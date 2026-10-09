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
function l() {
	return {
		verb: "rerun",
		fromStep: 0,
		edits: [],
		thread: "ephemeral",
		input: ""
	};
}
function u() {
	return {
		verb: "continue",
		fromStep: 0,
		edits: [],
		thread: "fork",
		input: "",
		focus: "input"
	};
}
function d(e) {
	let t = 0;
	for (let n of e) n + 1 > t && (t = n + 1);
	return t;
}
function f(e) {
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
function p(e, t) {
	return t ? e : e - 1;
}
function m(e, t) {
	if (e <= 0) return "nothing kept · the whole turn runs again";
	let n = e === 1 ? "step 0 kept" : `steps 0–${e - 1} kept`;
	return t ? `${n} · the compacted prefix (what the model saw at step ${e})` : n;
}
function h(e) {
	if (!e.startsWith("weft_pt.")) return "";
	try {
		let t = e.slice(8).split(".")[0].replace(/-/g, "+").replace(/_/g, "/");
		return JSON.parse(atob(t + "=".repeat((4 - t.length % 4) % 4))).scope === "playground" ? "playground" : "read";
	} catch {
		return "read";
	}
}
function g(e, t) {
	return e.includes("playground") && h(t) !== "read";
}
//#endregion
//#region src/lib/scope.ts
var _ = [
	"session",
	"flow",
	"run"
], v = (e) => {
	try {
		return encodeURIComponent(e);
	} catch {
		return e.replace(/[%;=\x00-\x1f\x7f]/g, (e) => `%${e.charCodeAt(0).toString(16).toUpperCase().padStart(2, "0")}`);
	}
}, y = (e) => {
	try {
		return decodeURIComponent(e);
	} catch {
		return e;
	}
};
function b(e) {
	let t = [v(e.publicId)];
	for (let n of _) {
		let r = e[n];
		r && t.push(`${n}=${v(r)}`);
	}
	return t.join(";");
}
function x(e) {
	let [t = "", ...n] = String(e).split(";"), r = { publicId: y(t.trim()) };
	for (let e of n) {
		let t = e.indexOf("=");
		if (t < 0) continue;
		let n = e.slice(0, t).trim();
		if (!_.includes(n)) continue;
		let i = n, a = y(e.slice(t + 1).trim());
		a && r[i] === void 0 && (r[i] = a);
	}
	return r;
}
//#endregion
//#region src/panel/detect.ts
var ee = "Weft-Scope";
function S(e) {
	try {
		return typeof e == "function" && Function.prototype.toString.call(e).includes("[native code]");
	} catch {
		return !1;
	}
}
function te(e) {
	try {
		if (typeof e == "string") return e;
		if (e instanceof URL) return e.href;
		let t = e?.url;
		return typeof t == "string" ? t : "";
	} catch {
		return "";
	}
}
function C(e) {
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
	return C(e) && C(t);
}
function re(e) {
	let t;
	try {
		t = window.fetch;
	} catch {
		return null;
	}
	if (typeof t != "function") return null;
	let n = (e.isNativeFetch ?? S)(t), r = !0, i = (t, n) => {
		if (!r || !t || typeof t != "object") return;
		let i = t.headers;
		if (!i || typeof i.get != "function") return;
		let a = i.get(ee);
		if (!a) return;
		let o = t.url, s = e.pageURL ? e.pageURL() : location.href, c = new URL(typeof o == "string" && o ? o : te(n), s);
		if (!ne(c.href, s) || e.ignore?.(c.href)) return;
		let l = x(a);
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
], w = [
	"story",
	"request",
	"timeline",
	"raw"
], ae = "weft.devtools", T = (e, t, n) => Math.min(Math.max(e, t), Math.max(t, n)), oe = () => window.innerWidth || 1024, se = () => window.innerHeight || 768;
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
		].includes(e.side) && (t.side = e.side), w.includes(t.tab) || (t.tab = t.raw ? "raw" : "story"), t;
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
		le()?.setItem(ae, JSON.stringify(n));
	} catch {}
}
function me() {
	let e = !1;
	try {
		let t = le();
		e = t?.getItem("weft_debug") === "1", e && (t?.setItem(ae, JSON.stringify({
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
	e.w = T(e.w, Math.min(360, t - 16), t - 16), e.h = T(e.h, Math.min(280, n - 16), n - 16), e.x = T(e.x, 0, t - e.w), e.y = T(e.y, 0, n - e.h);
	let r = he(e.side) ? t : n, i = he(e.side) ? 360 : 280;
	e.d = T(e.d, Math.min(i, r - 16), r - 16);
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
		return b(e);
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
			l = x(t);
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
		if (typeof e == "string") return x(e);
		let t = Ue(e);
		if (t !== void 0) return x(t);
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
			let n = x(t);
			if (n.publicId && !/[;=]/.test(n.publicId)) return n;
		}
	} catch {}
	return null;
}
function $e(e, t = Xe.href()) {
	return e.detect === "off" ? !1 : e.detect.includes("headers") ? !0 : C(t) && C(e.endpoint) && !e.token.startsWith("weft_pt.");
}
function et(e, t = Xe.href()) {
	return e.detect === "off" ? !1 : e.detect.includes("markers") ? !0 : C(t) || tt(e.token) !== "read";
}
var tt = h;
function nt() {
	try {
		if (new URLSearchParams(location.search).get("weft") === "debug" || me()) return !0;
	} catch {}
	return !1;
}
//#endregion
//#region src/lib/honesty.ts
var E = {
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
}, rt = {
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
}, it = Object.keys(E);
function at(e) {
	return e !== void 0 && Object.prototype.hasOwnProperty.call(E, e);
}
function ot(e) {
	return e < 1024 ? `${e} B` : e < 1048576 ? `${(e / 1024).toFixed(1)} KiB` : `${(e / 1048576).toFixed(1)} MiB`;
}
function st(e) {
	if (typeof e != "object" || !e) return [];
	let t = e, n = [], r = t["weft.content"];
	(r === "stripped" || r === "none") && n.push({ hole: "stripped" }), r === "redacted" && n.push({ hole: "redacted" });
	let i = Number(t["weft.content.truncated_bytes"]);
	return Number.isFinite(i) && i > 0 && n.push({
		hole: "truncated",
		bytes: i
	}), n;
}
function ct(e) {
	return `${rt.truncated?.result_cap.reason ?? ""} (${ot(e)} cut)`;
}
function D(...e) {
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
		let t = it.indexOf(e);
		return t < 0 ? it.length : t;
	};
	return [...t.values()].sort((e, t) => n(e.hole) - n(t.hole));
}
function O(e) {
	let t = at(e.hole) ? E[e.hole] : void 0, n = at(e.hole) ? rt[e.hole] : void 0, r = e.cause && n && Object.hasOwn(n, e.cause) ? n[e.cause] : void 0;
	return {
		label: e.hole === "truncated" && e.bytes && !e.cause ? `shortened by the recorder: ${ot(e.bytes)} cut` : t?.label ?? e.hole,
		reason: e.reason || r?.reason || t?.reason || e.hole,
		fix: e.fix === "" ? void 0 : e.fix || r?.fix || t?.fix,
		tone: t?.tone ?? "loss"
	};
}
function lt(e) {
	let t = Array.isArray(e.holes) ? [...e.holes] : [];
	return e.requests_badge === "not_recorded" && t.push({ hole: "not_recorded" }), e.status === "interrupted" && t.push({ hole: "interrupted" }), D(t);
}
function ut(e) {
	let t = [];
	e.status === "interrupted" && t.push({ hole: "interrupted" });
	let n = Array.isArray(e.gaps) ? e.gaps : [];
	return n.length && e.status && e.status !== "running" && t.push({
		hole: "gap",
		reason: `${n.length} ${n.length === 1 ? "event" : "events"} missing (${n.length === 1 ? "position" : "positions"} ${n.slice(0, 8).join(", ")}${n.length > 8 ? ", …" : ""}): a destination dropped a batch`
	}), e.stop_reason === "max_tokens" && t.push({ hole: "max_tokens" }), t;
}
function dt(e) {
	return e === "succeeded" || e === "failed";
}
var ft = "usage at finish", k = [
	"system",
	"tool_calls",
	"tool_results",
	"text",
	"usage"
], pt = {
	system: "system",
	tool_calls: "tool calls",
	tool_results: "tool results",
	text: "text",
	usage: "usage"
};
function mt(e, t) {
	return `diff?${new URLSearchParams({
		a: e,
		b: t
	}).toString()}`;
}
var ht = {
	same: "same",
	changed: "changed",
	unknown: "not comparable",
	missing: "missing"
}, gt = {
	same: "both runs recorded this column and it is the same",
	changed: "both runs recorded this column and it differs",
	unknown: "a side did not record this column (its holes say why): the two were not compared",
	missing: "one run has no such step"
};
function _t(e, t) {
	return !e.a || !e.b || e.changes.includes("missing") ? "missing" : e.unknown.includes(t) ? "unknown" : e.changes.includes(t) ? "changed" : "same";
}
var vt = {
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
function yt(e) {
	if (at(e)) {
		let t = E[e];
		return {
			mark: e,
			label: t.label,
			title: t.fix ? `${t.reason} — fix: ${t.fix}` : t.reason,
			hole: e
		};
	}
	let t = Object.hasOwn(vt, e) ? vt[e] : void 0;
	return {
		mark: e,
		label: t?.label ?? e,
		title: t?.title ?? e
	};
}
function bt(e) {
	return e ? D(e.holes) : [];
}
function xt(e) {
	return D((Array.isArray(e.holes) ? e.holes : []).map((e) => e.hole === "truncated" && !e.cause ? {
		...e,
		cause: "response_cap"
	} : e));
}
function St(e, t = 160) {
	return e.length > t ? `${e.slice(0, t - 1)}…` : e;
}
function Ct(e) {
	return e == null ? "" : typeof e == "string" ? e : JSON.stringify(e);
}
function wt(e) {
	return !!e && e.system === null && e.holes.some((e) => e.hole === "hidden");
}
function Tt(e, ...t) {
	return e === "system" && t.some(wt) ? "hidden" : void 0;
}
function Et(e, t) {
	if (!e) return "—";
	switch (t) {
		case "system": return e.system === null ? wt(e) ? E.hidden.label : e.system_hash ? `#${e.system_hash.slice(0, 8)}` : "—" : e.system ? St(e.system) : "(empty)";
		case "tool_calls": return e.tool_calls.length ? St(e.tool_calls.map((e) => `${e.name}(${Ct(e.args)})`).join(", ")) : "(none)";
		case "tool_results": return e.tool_results.length ? St(e.tool_results.map((e) => `${e.is_error ? "error: " : ""}${e.content}`).join(" · ")) : "(none)";
		case "text": return e.text === null ? "—" : e.text ? St(e.text) : "(none)";
		case "usage": return `${e.usage.input_tokens}→${e.usage.output_tokens}`;
	}
}
function Dt(e) {
	let t = bt(e);
	return {
		side: e,
		marks: (e?.marks ?? []).filter((e) => !t.some((t) => t.hole === e)).map(yt),
		holes: t
	};
}
function Ot(e) {
	let t = {};
	for (let n of k) t[n] = _t(e, n);
	return t;
}
function kt(e) {
	let t = [...e.steps].sort((e, t) => e.step - t.step).map((e) => {
		let t = Ot(e);
		return {
			step: e.step,
			changed: e.changed,
			cells: t,
			changedColumns: k.filter((e) => t[e] === "changed").map((e) => pt[e]),
			a: Dt(e.a),
			b: Dt(e.b)
		};
	});
	return {
		a: e.a,
		b: e.b,
		rows: t,
		markers: t.filter((e) => e.changed).map((e) => e.step),
		holes: xt(e),
		unknown: t.reduce((e, t) => e + k.filter((e) => t.cells[e] === "unknown").length, 0)
	};
}
function At(e) {
	return k.every((t) => e.cells[t] === "missing") ? `changed at step ${e.step} · ${e.a.side ? "only in the base run" : "missing from the base run"}` : `changed at step ${e.step}${e.changedColumns.length ? ` · ${e.changedColumns.join(", ")}` : ""}`;
}
function jt(e) {
	let t = e.markers.length, n = e.rows.filter((e) => e.changed && k.every((t) => e.cells[t] === "missing")).length, r = t ? `${t} of ${e.rows.length} ${e.rows.length === 1 ? "step" : "steps"} changed${n ? ` (${n} only in one run)` : ""} · first at step ${e.markers[0]}` : `no step changed of ${e.rows.length}`;
	return e.unknown ? `${r} · ${e.unknown} ${e.unknown === 1 ? "cell" : "cells"} not comparable` : r;
}
//#endregion
//#region src/lib/api.ts
function Mt(e) {
	let t = e?.batches;
	return Array.isArray(t) ? { batches: t.map((e) => {
		let t = Nt(e.messages), n = !Array.isArray(e.messages) || t.length !== e.messages.length;
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
function Nt(e) {
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
function A(e) {
	return typeof e.badge == "string";
}
//#endregion
//#region src/lib/requests.ts
function Pt(e) {
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
function j(e) {
	return e.length > 12 ? e.slice(0, 12) : e;
}
function Ft(e) {
	let t = e.body.params, n = (e) => e == null ? "adapter default" : JSON.stringify(e);
	return [
		["temperature", n(t.temperature)],
		["top_p", n(t.top_p)],
		["max_tokens", n(t.max_tokens)],
		["stop", t.stop === void 0 && e.content === "stripped" ? "not recorded (stripped)" : n(t.stop)],
		["seed", n(t.seed)]
	];
}
function It(e) {
	return Ft(e).map(([e, t]) => `${e} ${t}`).join(" · ");
}
var Lt = "request not recorded by weft v0.9.0 or earlier", Rt = "this step ran, but no request record names it";
function zt(e) {
	return {
		reason: `the panel reads a run's first ${e} requests; this step's are past them`,
		fix: "open the run in Studio (⤢)"
	};
}
var Bt = "the transcript does not hold the messages this request counts", Vt = "no messages record names this request (its compaction view could not be recorded)", Ht = "diff not drawn: the prompt was cut", Ut = "not stored yet — the run is still running";
//#endregion
//#region src/lib/events.ts
function M(e) {
	return typeof e == "string" ? e : "";
}
function Wt(e, t) {
	return typeof e == "number" && Number.isInteger(e) && e >= 0 ? e : t;
}
function Gt(e) {
	return typeof e == "number" && Number.isFinite(e) && e > 0;
}
function Kt(e) {
	let t = typeof e == "object" ? e : null;
	return {
		...t,
		input_tokens: t?.input_tokens ?? 0,
		output_tokens: t?.output_tokens ?? 0
	};
}
function N() {
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
			let f = st(d);
			f.length && (t[i] = f, e.holes = D(e.holes, f));
			let p, m;
			if (typeof l == "object" && l) {
				switch (l.type) {
					case "run_start":
						e.runId = M(l.id), e.agent = typeof l.agent == "string" ? l.agent : void 0, e.model = l.model, e.startPos = i;
						break;
					case "step_start":
						a = !0, p = o(Wt(l.index, c()));
						break;
					case "text_delta":
						p = o(c()), p.text += M(l.text);
						break;
					case "reasoning_delta":
						p = o(c()), p.reasoning += M(l.text);
						break;
					case "tool_args_delta":
						n.set(l.name, (n.get(l.name) ?? "") + M(l.args));
						break;
					case "tool_start":
						p = o(c()), m = {
							callId: M(l.call_id),
							name: M(l.name),
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
								content: M(l.content),
								isError: !!l.is_error
							}, t.state = "done", t.finishPos = i;
							for (let n of e.steps) n.toolCalls.includes(t) && (p = n, i > n.to && (n.to = i));
						}
						break;
					}
					case "step_finish":
						p = o(Wt(l.index, c())), p.finish = {
							reason: M(l.reason),
							raw: l.raw,
							usage: Kt(l.usage)
						}, Gt(l.latency_ms) && (p.finish.latencyMs = l.latency_ms), Gt(l.ttft_ms) && (p.finish.ttftMs = l.ttft_ms);
						break;
					case "steered": {
						let e = o(Wt(l.step, c()));
						p = e;
						let t = (Array.isArray(l.messages) ? l.messages : []).map((e) => Yt(e)).filter(Boolean).join("\n");
						e.steer = {
							text: (e.steer?.text ? e.steer.text + "\n" : "") + t,
							pos: i
						};
						break;
					}
					case "run_finish": e.finished = !0, e.usage = Kt(l.usage), e.pending = Array.isArray(l.pending) ? l.pending : [], e.finishPos = i;
				}
				f.length && (p && (p.holes = D(p.holes, f)), m && (m.holes = D(m.holes, f)));
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
function qt(e, t) {
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
function Jt(e) {
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
function Yt(e, t = "") {
	let n = e?.content;
	return Array.isArray(n) ? n.filter((e) => e?.type === "text" && typeof e.text == "string").map((e) => e.text).join(t) : "";
}
function Xt(e) {
	let t = Array.isArray(e) ? e : [], n = Zt(t);
	return t.map((e, t) => {
		let r = Jt(e), i = e?.step, a = e?.input, o = e?.badge;
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
function Zt(e) {
	let t = -1;
	return e.map((e, n) => {
		let r = Jt(e), i = e?.input, a = typeof i == "boolean" ? i : n === 0 && (r.length !== 1 || r[0].role !== "assistant");
		return !a && r.some((e) => e.role === "assistant") && t++, {
			step: Math.max(t, 0),
			input: a
		};
	});
}
function Qt(e) {
	let t = [], n = [];
	for (let r of Xt(e)) (r.input ? t : n).push(...r.messages);
	return {
		input: t,
		produced: n
	};
}
function $t(e) {
	let { input: t } = Qt(e);
	for (let e = t.length - 1; e >= 0; e--) {
		if (t[e].role !== "user") continue;
		let n = Yt(t[e], "\n");
		if (n) return n;
	}
	return null;
}
function en(e, t, n) {
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
	for (let e of Xt(t)) {
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
			let i = Yt(t);
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
function tn(e, t, n) {
	n?.status === "running" && (n = void 0);
	let r = [...e.holes ?? []];
	return e.derived && r.push({
		hole: "derived",
		reason: "this step's words come from a transcript batch whose step was inferred, not stored"
	}), e.finish?.reason === "max_tokens" && r.push({ hole: "max_tokens" }), D(Array.isArray(n?.holes) ? n.holes : [], r, (t ?? []).filter((e) => e.hole === "not_recorded" || e.hole === "stripped"));
}
function nn(e, t) {
	return D(Array.isArray(e?.holes) ? e.holes : [], t.holes);
}
function rn(e, t) {
	return e.state === "done" ? "done" : t === "running" ? "running" : "never";
}
var an = /…\[truncated (\d+) bytes\]/u, on = /^tool call (.+) was not executed: the response hit the output token limit$/, sn = "this call was not executed: the response hit the output token limit, and the loop retried the step with a full budget";
function cn(e) {
	let t = an.exec(e);
	if (t) return {
		kind: "bytes",
		bytes: Number(t[1])
	};
	let n = on.exec(e);
	return n ? {
		kind: "call",
		tool: n[1]
	} : null;
}
//#endregion
//#region src/lib/experiment-body.ts
function ln(e) {
	let t = Xt(e), n = Array.isArray(e) ? e : [], r = n.length > 0 && n.every((e) => typeof e.step == "number" && e.step >= 0 && typeof e.input == "boolean"), i = [];
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
function un(e) {
	let t = ln(e), n = t.filter((e) => e.m.role === "assistant"), r = d(n.map((e) => e.step)), i = f(t.map((e) => e.m));
	return {
		stepCount: r,
		answeredCalls: i,
		lastCalls: n.at(-1)?.m.content.some((e) => e.type === "tool_call") ?? !1,
		max: p(r, i)
	};
}
var dn = () => ({
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
}), P = {
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
}, fn = [
	"max_steps",
	"parallelism",
	"temperature",
	"top_p",
	"max_tokens",
	"seed"
], pn = [
	"top_p",
	"max_tokens",
	"seed",
	"stop",
	"tool_choice",
	"park_on",
	"only_tools"
], mn = "runtime predates the option lab: upgrade weft/runtime to use params, tool_choice, park_on, only_tools";
function hn(e) {
	return !e.defaults?.tool_choice.mode;
}
function F(e, t) {
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
var gn = (e) => e.split("\n").filter((e) => e !== ""), _n = (e, t) => t.filter((t) => e.includes(t));
function vn(e, t, n) {
	let r = {}, i = [], a = t.defaults, o = t.tools.map((e) => e.name), s = (e, t) => i.push({
		field: e,
		message: t
	});
	e ||= dn();
	let c = {}, l = {};
	for (let t of fn) {
		let n = e[t].trim();
		if (!n) continue;
		let r = Number(n), i = F(t, a);
		if (!Number.isFinite(r)) {
			s(t, `${t} must be a number`);
			continue;
		}
		if (i !== "" && Number(i) === r) continue;
		let o = Number.isSafeInteger(r);
		t === "max_steps" || t === "parallelism" ? (!o || r < 1 || r > 1e6 ? s(t, `${t} must be a whole number from 1`) : i !== "" && r > Number(i) && s(t, `${t} may only lower the agent's cap`), c[t] = r) : t === "temperature" ? ((r < 0 || r > 2) && s(t, "temperature must be between 0 and 2"), c[t] = r) : t === "top_p" ? ((r < 0 || r > 1) && s(t, "top_p must be between 0 and 1"), l.top_p = r) : t === "max_tokens" ? (o ? r <= 0 && s(t, "max_tokens must be positive") : s(t, "max_tokens must be a whole number"), l.max_tokens = r) : (o || s(t, "seed must be a whole number"), l.seed = r);
	}
	let u = gn(e.stop);
	u.length && u.join("\n") !== F("stop", a) && (u.length > 4 && s("stop", `stop takes at most 4 sequences, got ${u.length}`), l.stop = u);
	let d = {};
	l.top_p !== void 0 && (d.top_p = l.top_p), l.max_tokens !== void 0 && (d.max_tokens = l.max_tokens), l.stop && (d.stop = l.stop), l.seed !== void 0 && (d.seed = l.seed);
	for (let n of ["only_tools", "park_on"]) for (let r of new Set(e[n])) o.includes(r) || s(n, `tool ${r} in ${n} is not in agent ${t.name}'s manifest`);
	let f = _n(e.only_tools, o);
	f.length === o.length && (f = []);
	for (let e of f) n.length && !n.includes(e) && s("only_tools", `only_tools may only narrow tools_enabled: tool ${e} is not enabled`);
	let p = _n(e.park_on, o), m = (e) => f.length ? f.includes(e) : !n.length || n.includes(e), h;
	if (e.tool_choice && (h = e.tool_choice === "named" ? {
		mode: "named",
		name: e.tool_choice_name
	} : { mode: e.tool_choice }, a && h.mode === a.tool_choice.mode && (h.mode !== "named" || h.name === a.tool_choice.name) && (h = void 0)), h?.mode === "named") {
		let e = h.name ?? "";
		e ? o.includes(e) ? m(e) ? p.includes(e) && s("tool_choice", `tool_choice names ${e}, which park_on parks: every forced call would park`) : s("tool_choice", `tool_choice names ${e}, which this command turns off`) : s("tool_choice", `tool ${e} in tool_choice is not in agent ${t.name}'s manifest`) : s("tool_choice", "tool_choice named needs a tool name");
	} else !h && a?.tool_choice.mode === "named" && a.tool_choice.name && !m(a.tool_choice.name) && s("tool_choice", `the agent's default tool_choice names ${a.tool_choice.name}, which this command turns off; send tool_choice`);
	return Object.keys(c).length && (r.options = c), Object.keys(d).length && (r.params = d), h && (r.tool_choice = h), p.length && (r.park_on = p), f.length && (r.only_tools = f), hn(t) && (r.params || r.tool_choice || r.park_on || r.only_tools) && i.unshift({
		field: "lab",
		message: mn
	}), {
		overrides: r,
		problems: i
	};
}
function yn(e, t, n) {
	if (!e) return !1;
	let r = e[t];
	if (Array.isArray(r)) return r.length > 0;
	if (t === "stop") return gn(r).length > 0 && gn(r).join("\n") !== F(t, n);
	if (t === "tool_choice") return r !== "" && (r !== F(t, n) || r === "named" && e.tool_choice_name !== F("tool_choice_name", n));
	if (t === "tool_choice_name") return !1;
	let i = F(t, n);
	return r.trim() !== "" && (i === "" || Number(i) !== Number(r));
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
var bn = (e) => e.nodeType === 1 ? e.getAttribute("data-key") : null, xn = (e, t) => e.nodeName === t.nodeName && bn(e) === bn(t), Sn = (e) => e.nodeType === 1 ? e.className : "";
function Cn(e, t) {
	let n = Array.from(e.childNodes), r = /* @__PURE__ */ new Map(), i = [];
	for (let e of n) {
		let t = bn(e);
		t === null ? i.push(e) : r.set(t, [...r.get(t) ?? [], e]);
	}
	let a = t.map((e) => {
		let t = bn(e);
		if (t !== null) {
			let n = r.get(t)?.shift();
			return [e, n && xn(n, e) ? n : void 0];
		}
		let n = i.findIndex((t) => xn(t, e) && Sn(t) === Sn(e));
		return n < 0 && (n = i.findIndex((t) => xn(t, e))), [e, n < 0 ? void 0 : i.splice(n, 1)[0]];
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
		r === c ? c = c.nextSibling : e.insertBefore(r, c), n && wn(n, t);
	}
}
function wn(e, t) {
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
	Cn(n, Array.from(r.childNodes)), /^(INPUT|TEXTAREA|SELECT)$/.test(n.tagName) && n.value !== r.value && (n.value = r.value), n.tagName === "INPUT" && n.checked !== r.checked && (n.checked = r.checked);
}
function Tn(e, t) {
	return JSON.stringify(e, null, t);
}
function En(e) {
	try {
		return Tn(e, 2) ?? String(e);
	} catch {
		return String(e);
	}
}
function Dn(e) {
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
function On(e) {
	let { parsed: t, from: n, to: r } = Dn(e);
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
function kn(e, t) {
	return t ? `${e} — fix: ${t}` : e;
}
function R(e, t = {}) {
	let n = O({
		hole: e,
		...t
	}), r = I("span", `weft-badge ${n.tone === "loss" ? "weft-warn-badge" : "weft-info"}`, t.label ?? n.label, {
		title: kn(n.reason, n.fix),
		"data-hole": e
	});
	return r.appendChild(I("span", "weft-sr", ` — ${kn(n.reason, n.fix)}`)), r;
}
function An(e) {
	if (!e.length) return null;
	let t = I("span", "weft-holes");
	for (let n of e) t.appendChild(R(n.hole, n));
	return t;
}
function jn(e) {
	let t = O(e), n = R(e.hole, e);
	return n.lastChild?.remove(), I("div", `weft-note${t.tone === "loss" ? " weft-warn" : ""}`, [n, document.createTextNode(` — ${t.reason}${t.fix ? ` · fix: ${t.fix}` : ""}`)]);
}
function Mn(e, t) {
	let n = e === "not_recorded" && !t ? Lt : O({ hole: e }).label;
	return n.startsWith("request") ? n : `request: ${n}`;
}
function Nn(e, t = {}) {
	let n = O({
		hole: e,
		...t
	}), r = R(e, {
		...t,
		label: t.label ?? Mn(e, t.cause)
	});
	return r.lastChild?.remove(), [r, I("div", "weft-reason", [n.reason, n.fix && `fix: ${n.fix}`].filter(Boolean).join(" — "))];
}
function Pn(e) {
	return R("truncated", {
		label: `request: ${E.truncated.label} — first ${e.toLocaleString("en-US").replace(",", " ")} requests`,
		...zt(e)
	});
}
function Fn(e) {
	return e.kind === "bytes" ? R("truncated", {
		cause: "result_cap",
		reason: ct(e.bytes)
	}) : R("max_tokens", {
		reason: sn,
		fix: ""
	});
}
function In(e, t) {
	let n = O({
		hole: "not_recorded",
		cause: "no_public_id",
		reason: e,
		fix: t
	});
	return `${n.label}: ${n.reason} · fix: ${n.fix}`;
}
var Ln = {
	none: "captured none (weft.Content(false), or no destination takes content)",
	stripped: "otel.NoContent()"
};
function Rn(e, t, n = 0) {
	let r = e?.events.find((e) => typeof e.attrs?.["weft.content"] == "string")?.attrs?.["weft.content"], i = t?.content?.latest, a = !r && i && (!e || i.run_id === e.id) ? i : null, o = r ?? a?.mark ?? "", s = e?.capped && n ? ` · first ${n} events` : "";
	if (Object.hasOwn(Ln, o)) {
		let e = O({
			hole: "stripped",
			reason: a?.note,
			fix: a?.fix
		});
		return {
			text: `content off · ${Ln[o]}${s}`,
			title: kn(e.reason, e.fix),
			hole: "stripped"
		};
	}
	let c = 0, l = 0;
	for (let t of Object.values(e?.folded.eventHoles ?? {})) for (let e of t) e.hole === "truncated" && (c++, l += e.bytes ?? 0);
	if (c) {
		let e = O({ hole: "truncated" });
		return {
			text: `content on · ${c} ${c === 1 ? "event" : "events"} shortened (${ot(l)} cut)${s}`,
			title: kn(e.reason, e.fix),
			hole: "truncated"
		};
	}
	return o === "full" ? {
		text: `content on${s}`,
		title: a?.note || "the record marks this run's content stored in full"
	} : null;
}
var zn = {
	provider: "weft/runtime",
	name: "scripted"
};
function Bn(e, t) {
	let n = [], r = (e, t, r) => n.push(I("span", "weft-chip weft-tag", e, {
		title: t,
		"data-weft-chip": r
	}));
	e.model.provider === zn.provider && e.model.name === zn.name && r(e.usage.input_tokens + e.usage.output_tokens === 0 ? "scripted (0 tokens)" : "scripted", "model weft/runtime/scripted: the scripted engine replayed the source run's recorded turns", "scripted");
	let i = e.meta["weft.session.forked_from"];
	if (i && r(`fork of ${i}`, `weft.session.forked_from = ${i}: a thread fork's run (<session>#<entry>)`, "fork"), e.forked_from) {
		let [n, i] = e.forked_from.split("#");
		r(`experiment of ${t(n)}`, `weft.forked_from = ${e.forked_from}: a playground experiment of run ${n}${i ? ` from step ${i}` : ""}${e.experiment_id ? ` (weft.experiment.id ${e.experiment_id})` : ""}`, "experiment");
	} else e.experiment_id && r("experiment", `weft.experiment.id = ${e.experiment_id}`, "experiment");
	return n;
}
function Vn(e) {
	return {
		hole: "derived",
		reason: `the step's catalog could not be read (${e}): the list is the runtime's registration`
	};
}
function Hn(e, t) {
	let n = jn(e);
	return n.setAttribute("data-weft-catalog-hole", e.hole), n.appendChild(I("div", "weft-reason", t ? "the step's catalog is not readable here: the verdicts below cover the tools the runtime registers" : "the step's catalog is not readable here, and no runtime registers the agent")), n;
}
function Un() {
	return {
		hole: "not_recorded",
		reason: "the step carries no request record: the tools it offered are unknown"
	};
}
function Wn(e) {
	return {
		hole: "truncated",
		...zt(e)
	};
}
function Gn() {
	return {
		hole: "not_recorded",
		reason: `request: ${Ut}`
	};
}
function Kn() {
	return {
		hole: "not_recorded",
		cause: "not_served"
	};
}
function qn() {
	return {
		hole: "not_recorded",
		reason: "the step carries no request record: the system prompt it was called with is unknown"
	};
}
function Jn(e) {
	return {
		hole: "derived",
		reason: `the step's prompt could not be read (${e}): the registered prompt is pre-filled`
	};
}
//#endregion
//#region src/lib/live.ts
function Yn(e) {
	let [[t, n]] = Object.entries(e);
	return `${encodeURIComponent(t)}=${encodeURIComponent(n)}`;
}
var Xn = 6e4, Zn = class extends Error {
	status;
	constructor(e) {
		super(`live grant refused: ${e}`), this.status = e;
	}
};
function Qn(e) {
	return (e ?? ["event", "run"]).join(",");
}
async function $n(e, t, n, r, i) {
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
			kinds: Qn(r)
		}),
		signal: i
	});
	if (!o.ok) throw new Zn(o.status);
	let s = await o.json();
	if (typeof s?.sig != "string" || !s.sig) throw new Zn(o.status);
	return {
		sig: s.sig,
		exp: er(s.exp, o.headers.get("Date"))
	};
}
function er(e, t, n = Date.now()) {
	let r = typeof e == "string" ? Date.parse(e) : NaN, i = t ? Date.parse(t) : NaN;
	return !Number.isFinite(r) || !Number.isFinite(i) ? n + Xn : n + Math.min(Xn, Math.max(0, r - i - 1e3));
}
function tr(e, t, n, r) {
	let i = new URL(e);
	return i.search = Yn(t), i.searchParams.set("kinds", Qn(n)), i.searchParams.set("sig", r.sig), i.toString();
}
function nr(e, t = Date.now()) {
	return t >= e.exp;
}
//#endregion
//#region src/panel/client.ts
function rr(e, t) {
	return new URL(t, new URL("api/", e.base)).toString();
}
var z = class extends Error {
	status;
	code;
	body;
	constructor(e, t, n, r) {
		super(n), this.status = e, this.code = t, this.body = r;
	}
};
async function B(e, t, n) {
	let r = { Accept: "application/json" };
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(rr(e, t), {
		headers: r,
		signal: n
	});
	if (!i.ok) {
		let e = "network", t = `${i.status} ${i.statusText}`, n;
		try {
			let r = await i.json();
			n = r, r.error && (e = r.error.code ?? e, t = r.error.message ?? t);
		} catch {}
		throw new z(i.status, e, t, n);
	}
	return await i.json();
}
function ir(e, t) {
	return B(e, "meta", t);
}
function ar(e, t, n, r) {
	return B(e, mt(t, n), r);
}
function or(e, t, n) {
	let r = new URLSearchParams(t).toString();
	return B(e, `runs${r ? `?${r}` : ""}`, n);
}
function sr(e, t, n) {
	return B(e, `runs/${encodeURIComponent(t)}`, n);
}
function cr(e, t, n, r) {
	return B(e, `runs/${encodeURIComponent(t)}/events?after=${n}&limit=500`, r);
}
function lr(e, t, n) {
	return B(e, `runs/${encodeURIComponent(t)}/transcript`, n).then(Mt);
}
var ur = 1e3;
async function dr(e, t, n) {
	let r = [], i = 0;
	for (let a = 0; a < 10; a++) {
		let a;
		try {
			a = await B(e, `runs/${encodeURIComponent(t)}/requests?limit=${ur}${i ? `&from=${i}` : ""}`, n);
		} catch (e) {
			let t = e instanceof z && e.status === 403 ? e.body : null;
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
async function fr(e, t) {
	let n = await B(e, `runs/${encodeURIComponent(t)}/tools`).catch(() => null);
	return n?.badge === "stripped" ? {
		badge: n.badge,
		reason: n.reason,
		fix: n.fix
	} : void 0;
}
function pr(e, t, n) {
	return B(e, `runs/${encodeURIComponent(t)}/spans`, n);
}
function mr(e, t, n) {
	let r = new URLSearchParams(t).toString();
	return B(e, `sessions${r ? `?${r}` : ""}`, n);
}
function hr(e, t, n) {
	return B(e, `sessions/${encodeURIComponent(t)}/public_id`, n);
}
function gr(e, t) {
	return B(e, "runtimes", t);
}
function _r(e, t) {
	return Cr(e, "playground/runs", t);
}
function vr(e, t) {
	return B(e, `playground/commands/${encodeURIComponent(t)}`);
}
function yr(e, t, n) {
	return Cr(e, `runs/${encodeURIComponent(t)}/approvals`, n);
}
function br(e, t, n) {
	return Sr(e, `runtimes/${encodeURIComponent(t)}/breakpoints`, { tools: n });
}
function xr(e, t, n) {
	return Cr(e, `runs/${encodeURIComponent(t)}/steer`, { message: n });
}
async function Sr(e, t, n) {
	let r = {
		Accept: "application/json",
		"Content-Type": "application/json"
	};
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(rr(e, t), {
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
		throw new z(i.status, e, t);
	}
	return await i.json();
}
async function Cr(e, t, n) {
	let r = {
		Accept: "application/json",
		"Content-Type": "application/json"
	};
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(rr(e, t), {
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
		throw new z(i.status, e, t);
	}
	return await i.json();
}
var wr = 1e4;
function Tr(e, t) {
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
		$n(rr(e, "live-grant"), e.token, t.selector, t.kinds).then((e) => {
			!n && r === o && f(e);
		}, (e) => {
			if (!(n || r !== o)) {
				if (t.onRefused && e instanceof Zn && e.status === 403) {
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
			f = new EventSource(tr(rr(e, "live"), t.selector, t.kinds, o));
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
				if (r = !0, f.readyState === EventSource.CLOSED || nr(o)) {
					if (f.close(), s >= 1) {
						u("closed");
						return;
					}
					s++, d();
				}
				i ||= setTimeout(() => {
					i = null, !(n || a?.readyState === EventSource.OPEN) && u("closed");
				}, wr);
			}
		};
	};
	return d(), { close: l };
}
//#endregion
//#region src/lib/diff.ts
function Er(e, t) {
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
function Dr(e) {
	let t = e.filter((e) => e.kind === "add").length, n = e.filter((e) => e.kind === "del").length;
	return !t && !n ? "identical" : `+${t} −${n}`;
}
var Or = 2e3;
function kr(e, t, n = Or) {
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
	})) : Er(s.join("\n"), c.join("\n"));
	return { rows: [
		...l(r.slice(0, a)),
		...u,
		...l(r.slice(r.length - o))
	] };
}
//#endregion
//#region src/lib/links.ts
function Ar(e) {
	return typeof e == "number" && Number.isInteger(e) && e >= 0 ? e : void 0;
}
function jr(e, t = {}) {
	let n = Ar(t.step), r = {};
	n !== void 0 && (r.step = n), t.view && t.view !== "trace" && (r.view = t.view), t.call && (t.resumed || n !== void 0) ? r.sel = `c:${t.resumed ? "resume" : String(n)}:${t.call}` : t.span && (r.sel = `t:${t.span}`), t.axis === "time" ? r.axis = "time" : t.axis === "events" && (r.axis = "events");
	let i = Ar(t.t);
	return i !== void 0 && (r.t = i), {
		to: "/runs/$id",
		params: { id: e },
		search: r
	};
}
function Mr(e) {
	return {
		to: "/sessions/$id",
		params: { id: e },
		search: {}
	};
}
function Nr(e, t = {}) {
	return {
		to: "/traces/$id",
		params: { id: e },
		search: t.span ? { span: t.span } : {}
	};
}
function Pr(e = {}) {
	let t = new URLSearchParams();
	for (let [n, r] of Object.entries(e)) r !== void 0 && r !== "" && (n !== "step" || Ar(r) !== void 0) && t.set(n, String(r));
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
function Fr(e, t = [], n) {
	let r = [...new Set(t.filter((t) => t && t !== e))], i = { a: e };
	r.length && (i.b = r);
	let a = Ar(n);
	return a !== void 0 && (i.step = a), {
		to: "/compare",
		search: i
	};
}
function Ir(e) {
	if (typeof e == "string") try {
		return JSON.parse(e), JSON.stringify(e);
	} catch {
		return e;
	}
	return typeof e == "object" ? JSON.stringify(e) : String(e);
}
function Lr(e) {
	let t = e.to.replace(/^\//, "");
	"params" in e && (t = t.replace("$id", encodeURIComponent(e.params.id)));
	let n = new URLSearchParams();
	for (let [t, r] of Object.entries(e.search)) r !== void 0 && n.set(t, Ir(r));
	let r = n.toString(), i = "hash" in e && e.hash ? `#${e.hash}` : "";
	return `${t}${r ? `?${r}` : ""}${i}`;
}
function V(e, t) {
	return new URL(Lr(t), e).toString();
}
//#endregion
//#region src/panel/compare.ts
function Rr(e, t) {
	if (!t.marks.length && !t.holes.length) return null;
	let n = I("span", "weft-holes", `${e} `, { "data-weft-diff-side": e });
	for (let e of t.marks) n.appendChild(e.hole ? R(e.hole) : I("span", "weft-chip weft-tag", e.label, {
		title: e.title,
		"data-weft-diff-mark": e.mark
	}));
	let r = An(t.holes);
	return r && n.appendChild(r), n;
}
function zr(e, t) {
	let n = kt(e), r = I("div", "weft-diff", void 0, {
		"data-weft-step-diff": "",
		"data-key": "step-diff"
	}), i = I("a", "weft-btn", "⤢", {
		href: V(t, Fr(n.a.run_id, [n.b.run_id], n.markers.at(0))),
		target: "_blank",
		rel: "noopener",
		"aria-label": `open the step compare of ${n.a.run_id} and ${n.b.run_id} in Studio`
	});
	i.style.textDecoration = "none", r.appendChild(I("div", "weft-diff-h", [document.createTextNode(`steps vs source: ${jt(n)} `), i]));
	for (let e of n.holes) r.appendChild(jn(e));
	if (n.markers.length) {
		let e = I("ul", "weft-diff-markers", void 0, { "aria-label": "changed steps" });
		for (let r of n.rows) if (r.changed) {
			let i = I("a", void 0, At(r), {
				href: V(t, jr(n.b.run_id, { step: r.step })),
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
	for (let e of k) o.appendChild(I("th", void 0, pt[e], { scope: "col" }));
	a.appendChild(I("thead", void 0, [o]));
	let s = I("tbody");
	for (let e of n.rows) {
		let t = I("tr", e.changed ? "weft-diff-changed" : void 0, void 0, { "data-weft-diff-step": String(e.step) }), n = I("th", void 0, String(e.step), { scope: "row" });
		for (let [t, r] of [["a", e.a], ["b", e.b]]) {
			let e = Rr(t, r);
			e && n.appendChild(e);
		}
		t.appendChild(n);
		for (let n of k) {
			let r = e.cells[n], i = I("td", `weft-diff-${r}`, ht[r], {
				"data-weft-diff-cell": n,
				"data-state": r,
				title: gt[r]
			}), a = Tt(n, e.a.side, e.b.side);
			a && (i.appendChild(document.createTextNode(" ")), i.appendChild(R(a))), r === "changed" && (i.appendChild(I("div", "weft-diff-row weft-diff-a", `a ${Et(e.a.side, n)}`)), i.appendChild(I("div", "weft-diff-row weft-diff-b", `b ${Et(e.b.side, n)}`))), t.appendChild(i);
		}
		s.appendChild(t);
	}
	return a.appendChild(s), r.appendChild(a), r;
}
//#endregion
//#region src/lib/format.ts
function Br(e, t = Date.now()) {
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
function Vr(e, t) {
	let n = Date.parse(e), r = t ? Date.parse(t) : NaN;
	return Number.isNaN(n) || Number.isNaN(r) || r < n ? "—" : Hr(r - n);
}
function Hr(e) {
	if (!Number.isFinite(e)) return "—";
	if (e < 1e3) return `${Math.round(e)}ms`;
	if (e < 59950) return `${(e / 1e3).toFixed(1)}s`;
	let t = Math.round(e / 1e3), n = Math.floor(t / 60);
	return n < 60 ? `${n}m${String(t % 60).padStart(2, "0")}s` : `${Math.floor(n / 60)}h${String(n % 60).padStart(2, "0")}m`;
}
function H(e) {
	if (!Number.isFinite(e)) return "—";
	let t = Math.abs(e);
	return t >= 999950 ? `${(e / 1e6).toFixed(1)}M` : t >= 1e3 ? `${(e / 1e3).toFixed(1)}k` : String(e);
}
//#endregion
//#region src/lib/compaction.ts
var Ur = (e, t) => `${e} ${t}${e === 1 ? "" : "s"}`;
function U(e) {
	return e.scope === "session";
}
function Wr(e) {
	return Array.isArray(e?.compactions) ? e.compactions : [];
}
function Gr(e) {
	if (U(e)) {
		let t = e.tokens_before && e.tokens_after ? ` · ${H(e.tokens_before)} → ${H(e.tokens_after)} tokens` : "";
		return `${Ur(e.replaced, "message")} compacted into ${e.entries}${t}`;
	}
	return e.replaced === 0 ? `${Ur(e.entries, "message")} inserted by PrepareStep` : `${Ur(e.replaced, "message")} rewritten into ${e.entries} by PrepareStep`;
}
var Kr = "session compaction · after this run";
function qr(e) {
	return `thread compacted the session context this run belongs to: ${e.replaced} of its messages were replaced by ${e.entries}; the next run starts on the compacted context (its input record). The marker carries counts and a hash, never messages. (For entries appended by hand that no run produced, thread files the marker under the run that follows, which starts on the compacted context.)`;
}
function Jr(e, t, n) {
	if (!t) return { loading: !0 };
	let r = e.index ?? -1, i = e.from_seq ?? -1, a = e.to_seq ?? -1;
	if (r < 0 || i < 0 || a < i) return { gap: "the view names no usable range: its replaced messages cannot be placed" };
	let o = new Set(n.filter((e) => !U(e) && e.index != null).map((e) => e.index)), s = (Array.isArray(t.batches) ? t.batches : []).filter((e) => e !== null && typeof e.index == "number" && e.index < r).sort((e, t) => e.index - t.index), c = new Set(s.map((e) => e.index)), l = [];
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
function Yr(e, t = 160) {
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
function Xr(e, t) {
	let n = t == null ? "" : ` — the step's request carried ${Ur(t, "message")} in all`;
	return `in their place, ${Ur(e.entries, "message")}${n}; the view's body stays in its record (export the run: compactions[].messages)`;
}
//#endregion
//#region src/lib/attempts.ts
function Zr(e, t, n = !1) {
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
function Qr(e) {
	if (!e) return null;
	if (e.n === 0) return e.total > 0 ? `${e.total} ${e.total === 1 ? "attempt" : "attempts"} · none answered` : null;
	if (e.n <= 1) return null;
	let t = `attempt ${e.n} of ${Math.max(e.total, e.n)}`;
	return e.requested && e.answered && (t += e.answered === e.requested ? " · retry" : ` · fallback to ${e.answered}`), t;
}
function $r(e) {
	if (e < 1e3) return `${Math.round(e)} ms`;
	if (e < 59950) return `${(e / 1e3).toFixed(1)} s`;
	let t = Math.round(e / 1e3);
	return `${Math.floor(t / 60)}m${String(t % 60).padStart(2, "0")}s`;
}
function ei(e, t, n = "first token") {
	let r = [];
	return e && e > 0 && r.push($r(e)), t && t > 0 && r.push(`${n} ${$r(t)}`), r.length ? r.join(" · ") : null;
}
function ti(e, t) {
	return e === void 0 || e.latencyMs || t !== 0 ? null : {
		hole: "not_recorded",
		reason: "this step's step_finish has no timing and its request record no attempt rows: it was recorded by a weft before attempt reporting (A4)"
	};
}
//#endregion
//#region src/panel/markers.ts
var ni = "data-weft-scope", ri = "data-weft-untrusted", ii = (e) => !!e.closest(`[${ri}]`), ai = "weft-devtools";
function oi(e) {
	try {
		if (!(e instanceof Element) || e.closest(ai)) return null;
		let t = e.closest(`[${ni}]`);
		for (; t && ii(t);) t = t.parentElement?.closest("[data-weft-scope]") ?? null;
		return t;
	} catch {
		return null;
	}
}
function si(e) {
	if (e.type === "attributes") return !(e.target instanceof Element && e.target.closest(ai));
	for (let t of [...Array.from(e.addedNodes), ...Array.from(e.removedNodes)]) if (t instanceof Element && (t.hasAttribute("data-weft-scope") || t.querySelector("[data-weft-scope]"))) return !0;
	return !1;
}
function ci(e = document) {
	let t = [];
	for (let n of Array.from(e.querySelectorAll(`[${ni}]`))) {
		if (n.closest(ai) || ii(n)) continue;
		let e = x(n.getAttribute("data-weft-scope") ?? "");
		e.publicId && t.push({
			scope: e,
			element: n
		});
	}
	return t;
}
function li(e) {
	let t = e.root ?? document, n = null, r = null, i = 0, a = !1, o = () => {
		if (n = null, i = 0, t.hidden) {
			a = !0;
			return;
		}
		a = !1;
		try {
			e.onScopes(ci(t));
		} catch {}
	}, s = () => {
		a && !t.hidden && o();
	}, c = (t) => {
		let n = oi(t.target);
		if (n) try {
			e.onFocus?.(n);
		} catch {}
	};
	try {
		r = new MutationObserver((t) => {
			if (!t.some(si)) return;
			let r = Date.now();
			i ||= r, n && clearTimeout(n), n = setTimeout(o, Math.max(0, Math.min(e.debounceMs ?? 100, i + 500 - r)));
		}), r.observe(t.documentElement, {
			subtree: !0,
			childList: !0,
			attributes: !0,
			attributeFilter: [ni, ri]
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
var ui = {
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
}, di = "ui-monospace, SFMono-Regular, Menlo, Consolas, \"Liberation Mono\", monospace", fi = (e) => Object.entries(ui[e]).map(([e, t]) => `--weft-${e}: ${t};`).join(" ");
[...Object.keys(ui.dark).map((e) => `--weft-${e}`)];
var pi = `
:host { all: initial; color: inherit; box-sizing: border-box; color-scheme: dark; ${fi("dark")}
  --weft-font: ${di}; --weft-radius: 8px; --weft-radius-sm: 5px; }
:host([data-theme-resolved="light"]) { color-scheme: light; ${fi("light")} }
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
function mi(e) {
	return e.content.filter((e) => e?.type === "text").map((e) => e.text).join("");
}
function hi(e, t) {
	let n = "";
	if (t !== void 0) try {
		n = Tn(t) ?? "";
	} catch {
		n = "?";
	}
	return `${e}(${n})`;
}
function gi(e) {
	return e ? $t(e.batches) ?? "" : "";
}
function _i(e) {
	if (!e || !e.batches.length) return null;
	let { input: t, produced: n } = Qt(e.batches), r = t.length;
	for (let e = t.length - 1; e >= 0 && t[e].role !== "user"; e--) r = e;
	let i = [...t.slice(r), ...n].filter((e) => e.role === "assistant"), a = [];
	for (let e of i) for (let t of e.content) t?.type === "tool_call" && a.push(hi(t.name, t.args));
	return {
		text: i.map(mi).filter(Boolean).join("\n"),
		calls: a
	};
}
function vi(e) {
	return {
		text: e.steps.map((e) => e.text).filter(Boolean).join("\n"),
		calls: e.steps.flatMap((e) => e.toolCalls).map((e) => hi(e.name, e.args))
	};
}
function yi(e, t) {
	let n = /-t(\d+)$/.exec(e);
	return `${n ? `t${n[1]}` : e.slice(-8)}·x${t + 1}`;
}
function bi(e) {
	if (e.thread === "fork" && !(e.runId && e.input.trim() && e.step === 0)) return "fork continues the conversation in a new session: it needs a source run, an input, and step 0";
	let t = Object.keys(e.tools);
	return t.length && !t.some((t) => e.tools[t]) ? "at least one tool must stay on — the command cannot express an empty tool set (it would run with every tool)" : xi(e).problems[0]?.message ?? null;
}
function xi(e) {
	let t = Object.keys(e.tools), n = t.filter((t) => e.tools[t]);
	return vn(e.lab, {
		name: e.agent,
		defaults: e.defaults,
		tools: t.map((e) => ({ name: e }))
	}, n.length < t.length ? n : []);
}
function Si(e, t) {
	let n = Object.entries(e.tools).filter(([, e]) => e).map(([e]) => e), r = {};
	e.instructions && e.instructions !== e.registeredInstructions && (r.instructions = e.instructions), n.length && n.length < Object.keys(e.tools).length && (r.tools_enabled = n), e.model && (r.model = e.model), e.thinking && (r.thinking = e.thinking), Object.assign(r, xi(e).overrides);
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
	return e.step === 0 && e.input && (i.input = e.input), e.step > 0 && e.edits.length && (i.transcript_edits = e.edits.map((e) => ({
		step: e.step,
		...e.callID ? { call_id: e.callID } : {},
		...e.toolResult ? { tool_result: e.toolResult } : {},
		...e.content ? { content: e.content } : {}
	}))), t && (i.public_id = t), i;
}
function Ci(e, t) {
	let n = e.filter((e) => e.agents.some((e) => e.name === t));
	if (!n.length) return e[0] ?? null;
	let r = (e) => Date.parse(e.last_seen) || 0;
	return n.reduce((e, t) => r(t) > r(e) ? t : e);
}
//#endregion
//#region src/panel/version.ts
function wi() {
	return "v0.12.0";
}
function Ti(e) {
	if (typeof e != "string") return null;
	let t = /^v?(\d+(?:\.\d+)*)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]*)?$/.exec(e.trim());
	return t ? {
		nums: t[1].split(".").map(Number),
		pre: t.at(2) ?? ""
	} : null;
}
function Ei(e, t) {
	let n = Ti(e), r = Ti(t);
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
function Di(e) {
	return Ei(e, wi()) > 0;
}
//#endregion
//#region src/panel/state.ts
var Oi = (e, t) => !!e && e.before === t.before && e.id === t.id, ki = 1e3, Ai = 700, ji = 8, Mi = 1e4, Ni = 3e4, Pi = 1e3;
function Fi() {
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
		breakpoints: [],
		live: !1,
		devAgent: "",
		devRefused: !1,
		pinMissing: "",
		sessionUnrecorded: !1,
		listKey: ""
	};
}
function Ii(e) {
	let t = N();
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
function Li(e) {
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
function Ri(e, t, n, r) {
	if (t && typeof t == "object" && typeof t.type == "string") try {
		e.push(t, n, r);
	} catch {}
}
function zi(e, t, n) {
	for (let r of n) r && typeof r.pos == "number" && !t.has(r.pos) && (t.add(r.pos), Ri(e, r.event, r.pos, r.attrs));
}
function Bi(e, t) {
	let n = N(), r = /* @__PURE__ */ new Set();
	zi(n, r, t), e.feed = n, e.seen = r, e.events = t, e.pos = r.size ? Math.max(...r) : -1, e.stale = !0;
}
function Vi(e, t, n = !1) {
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
	return Ri(e.feed, t.event, r, t.attrs), e.stale = !0, "folded";
}
function W(e) {
	let t = e.result();
	return Array.isArray(t.pending) || (t.pending = []), t;
}
function Hi(e, t, n = !1) {
	if (!t) return e;
	try {
		return en(e, t.batches, { replace: n });
	} catch {
		return e;
	}
}
var G = () => {}, Ui = class {
	state = Fi();
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
		this.publicId = r.publicId, this.narrowing = qi(r);
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
			let t = await ir(this.ep);
			if (!t || typeof t != "object" || typeof t.studio_version != "string") throw Error("not a Studio");
			e = {
				...t,
				capabilities: Array.isArray(t.capabilities) ? t.capabilities : []
			};
		} catch {
			return this.state.gone = !0, !1;
		}
		return !this.disposed && (this.state.meta = e, this.state.tooNew = Di(e.studio_version), this.emit(), this.state.tooNew || await this.scope(), !0);
	}
	async rescope(e, t = {}) {
		let n = typeof e == "string" ? { publicId: e } : e, r = qi(n), i = b({
			publicId: "",
			...r
		}) === b({
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
		this.scopeSub = Tr(this.ep, {
			selector: { public_id: this.publicId },
			kinds: ["run"],
			onOpen: (e) => {
				this.retries.scope = 0, this.state.live || (this.state.live = !0, this.emit()), e && this.refresh().catch(G);
			},
			onRun: (e) => this.onRunFrame(e),
			onOverflow: (e) => {
				this.scopeSub = void 0, this.state.live = !1, this.emit(), e !== "expired" && (e === "overflow" ? this.overflowed("scope", t) : this.retry("scope", t));
			}
		}), this.state.live = !0, this.emit();
	}
	retry(e, t) {
		let n = this.retries[e];
		n >= 5 || (this.retries[e] = n + 1, this.after(Math.min(5e3 * 2 ** n, 6e4), () => void t().catch(G)));
	}
	overflowed(e, t) {
		let n = this.overflows[e];
		n.n = n.at && Date.now() - n.at < 6e4 ? n.n + 1 : 0, this.after(Math.min(ki * 2 ** Math.min(n.n, 6), 6e4), () => {
			this.overflows[e].at = Date.now(), t().catch(G);
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
		this.cancel(this.devTimer), this.devTimer = null, !(!this.watching || this.publicId || this.disposed || !this.state.meta || this.state.tooNew) && (this.devTimer = this.after(this.devSub ? Ni : Mi, () => {
			if (this.devTimer = null, typeof document < "u" && document.visibilityState === "hidden") {
				this.armDev();
				return;
			}
			this.refresh().catch(G).then(() => this.armDev());
		}));
	}
	armStale() {
		if (this.cancel(this.staleTimer), this.staleTimer = null, this.disposed || ![...this.state.turns, ...[...this.state.experiments.values()].flat()].some((e) => e.status === "running")) return;
		let e = this.state.meta?.interrupted_after_ms, t = (typeof e == "number" && e > 0 ? Math.min(e, 6e5) : 3e4) + 1e3;
		this.staleTimer = this.after(t, () => {
			this.staleTimer = null, this.refresh().catch(G);
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
		this.devSub = Tr(this.ep, {
			selector: { agent: n },
			kinds: ["run"],
			onOpen: (e) => {
				this.retries.dev = 0, e && this.refresh().catch(G);
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
				let t = await mr(this.ep, { public_id: this.publicId }).catch(() => null);
				if (e !== this.loadSeq || this.disposed) return;
				if (t && Array.isArray(t.sessions)) {
					let e = this.narrowing.session;
					this.state.session = (e ? t.sessions.find((t) => t.id === e) : t.sessions[0]) ?? null;
				}
			}
			let n = await or(this.ep, this.publicId ? {
				public_id: this.publicId,
				limit: "50"
			} : { limit: "10" });
			if (e !== this.loadSeq || this.disposed) return;
			let { turns: r, experiments: i } = Li((Array.isArray(n.runs) ? n.runs : []).filter((e) => !!e && typeof e.id == "string")), a = this.narrowing.session, o = this.state;
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
			this.state.listKey = Ki(this.publicId, this.narrowing.session);
		} catch {
			return;
		} finally {
			this.collectors.delete(t);
		}
		this.armStale(), this.publicId || (this.subscribeDev(e), this.armDev());
		let n = this.state.turn;
		if (n && !n.done && !n.loading) {
			let e = this.rowOf(n.id)?.status;
			e && e !== "running" && this.settleTurn(n).catch(G);
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
		return this.pinChecks++, t ? (this.state.pinMissing = "", this.pinApplied === e || this.userSelected ? "" : (this.pinApplied = e, e)) : (this.state.pinMissing = this.pinChecks >= 2 ? e : "", this.pinChecks === 1 && !this.pinTimer && (this.pinTimer = this.after(Pi, () => {
			this.pinTimer = null, this.refresh().catch(G);
		})), "");
	}
	onRunFrame(e) {
		let t = e.run;
		if (this.publicId && t.public_id && t.public_id !== this.publicId || t.parent_run_id || !this.inSession(t)) return;
		for (let e of this.collectors) e.push(t);
		this.upsertRun(t), this.publicId || this.capDev(), this.armStale();
		let n = this.takePin();
		if (n) {
			this.select(n).catch(G);
			return;
		}
		if (!this.state.selected && this.state.turns.length) {
			let e = this.state.turns.find((e) => e.status === "running");
			this.select((e ?? this.state.turns[0]).id).catch(G);
			return;
		}
		let r = this.state.turn;
		r && r.id === t.id && (t.status === "running" ? r.done && !this.runSub && !r.loading && (r.done = !1, this.resumeTurn(r).catch(G)) : this.settleTurn(r).catch(G)), this.emit();
	}
	upsertRun(e, t = !1) {
		if (t && this.rowOf(e.id)) return;
		let n = this.state.turns.filter((t) => t.id !== e.id), r = /* @__PURE__ */ new Map();
		for (let [t, n] of this.state.experiments) {
			let i = n.filter((t) => t.id !== e.id);
			i.length && r.set(t, i);
		}
		let i = Li([e, ...n]);
		this.state.turns = i.turns;
		for (let [e, t] of i.experiments) r.set(e, [...r.get(e) ?? [], ...t]);
		this.state.experiments = r;
	}
	async walkEvents(e) {
		let t = [], n = [], r = 0;
		for (let i = 0; i < 20; i++) {
			let i = await cr(this.ep, e, r);
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
		let n = Ii(e);
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
			sr(n, r).catch(() => null),
			this.walkEvents(r).catch(() => null),
			lr(n, r).catch(() => null),
			pr(n, r).then((e) => Array.isArray(e.spans) ? e.spans : null).catch(() => null),
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
				r && r !== n.status && (e.tried.delete(t), e.expanded.has(t) ? this.expandChild(t, !0).catch(G) : e.children.delete(t));
			}
			a && (Bi(e, a.events), e.gaps = a.gaps, e.capped = a.capped), o && (e.transcript = o), s && (e.spans = s), c && (e.requests = c), e.done || e.capped ? e.held.length = 0 : this.drain(e, r, () => this.state.turn === e && !e.done), e.recheck && !e.done && (e.recheck = !1, this.catchUp(e, r, () => this.state.turn === e && !e.done).catch(G)), this.dress(e), this.emit();
		}
	}
	async readRequests(e) {
		if (!this.state.meta?.capabilities.includes("requests")) return null;
		if (tt(this.ep.token) === "read") return {
			badge: "hidden",
			reason: E.hidden.reason,
			fix: E.hidden.fix,
			steps: /* @__PURE__ */ new Map()
		};
		try {
			let t = await dr(this.ep, e), n = t.requests.some((e) => e.content === "stripped") ? await fr(this.ep, e) : void 0;
			return {
				badge: t.badge,
				reason: t.reason,
				fix: t.fix,
				truncated: t.truncated,
				steps: Pt(t.requests),
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
		for (let i = 0; i < r.length; i++) if (Vi(e, r[i]) === "gap") {
			e.held.push(...r.slice(i)), this.catchUp(e, t, n).catch(G);
			return;
		}
	}
	liveInto(e, t, n, r) {
		if (e.loading || e.reading) {
			e.held.push(n);
			return;
		}
		let i = Vi(e, n);
		i === "gap" ? (e.held.push(n), this.catchUp(e, t, r).catch(G)) : i === "folded" && this.emit(!0);
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
					let i = await cr(this.ep, t, r);
					if (!n()) return;
					if (Array.isArray(i.events)) for (let t of i.events) t && typeof t.pos == "number" && !e.seen.has(t.pos) && (e.seen.add(t.pos), e.events.push(t), Ri(e.feed, t.event, t.pos, t.attrs), t.pos > e.pos && (e.pos = t.pos), e.stale = !0);
					if (typeof i.next_after != "number" || i.next_after <= r) break;
					r = i.next_after;
				}
			} while (r());
		} catch {} finally {
			e.reading = !1;
		}
		if (n() && !i()) {
			for (let t of e.held.splice(0)) Vi(e, t, !0);
			this.emit(!0);
		}
	}
	dress(e) {
		e.stale = !1;
		let t = this.rowOf(e.id)?.status ?? e.doc?.status;
		if (e.folded = Hi(W(e.feed), e.transcript, e.done || !!t && t !== "running"), e.doc && Array.isArray(e.doc.children)) try {
			qt(e.folded, e.doc.children);
		} catch {}
	}
	follow(e) {
		let t = e.id;
		this.runSub?.close(), this.runSub = Tr(this.ep, {
			selector: { run: t },
			kinds: [
				"event",
				"delta",
				"run"
			],
			onOpen: () => {
				this.retries.run = 0, this.state.turn === e && !e.done && !e.capped && this.catchUp(e, t, () => this.state.turn === e && !e.done).catch(G);
			},
			onRecord: (n) => {
				this.state.turn !== e || e.id !== n.run_id || e.done || e.capped || this.liveInto(e, t, n, () => this.state.turn === e && !e.done);
			},
			onRun: (n) => {
				n.run.id === t && this.state.turn === e && (this.rowOf(t) && this.upsertRun(n.run), n.run.status !== "running" && this.settleTurn(e).catch(G), this.emit());
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
		let a = N();
		zi(a, /* @__PURE__ */ new Set(), i.events);
		let [o, s, c] = await Promise.all([
			lr(this.ep, e).catch(() => null),
			sr(this.ep, e).catch(() => null),
			this.readRequests(e)
		]);
		if (r !== this.loadSeq || this.disposed || this.state.turn !== n) return;
		let l = n.doc?.children.find((t) => t.id === e)?.status ?? "running", u = Hi(W(a), o, l !== "running");
		if (s && Array.isArray(s.children)) try {
			qt(u, s.children);
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
			t = await sr(this.ep, e);
		} catch (e) {
			let t = e instanceof z ? e.status : 0;
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
			let e = await gr(this.ep);
			l = Array.isArray(e.runtimes) ? e.runtimes : [];
		} catch (e) {
			!this.disposed && c === this.loadSeq && this.setExperimentError(K(e), a);
			return;
		}
		if (this.disposed || c !== this.loadSeq) return;
		this.state.runtimes = l;
		let u = Ci(l, i), d = u?.agents.find((e) => e.name === i);
		if (!u || !d) {
			this.setExperimentError(`no connected runtime registers the agent "${i}"${e === a ? "" : ` (run ${e})`} — experiments run in your app (weft/runtime)`, a);
			return;
		}
		let f = {};
		for (let e of d.tools) f[e.name] = !0;
		this.state.breakpoints = Array.isArray(u.breakpoints) ? [...u.breakpoints] : [];
		let p = this.state.turn;
		if (p && e !== p.id && !p.children.has(e) && (await this.expandChild(e), this.disposed || c !== this.loadSeq)) return;
		let m = this.state.turn, h = m ? m.id === e ? m : m.children.get(e) : void 0, g = s === 0 && m && m.id === e ? gi(m.transcript) : "", _ = d.instructions ?? "", v = o?.instructions ?? _, y, b;
		if (o?.verb === "edit_prompt" && o.instructions === void 0) {
			let e = h?.requests?.steps.get(s)?.rows, t = e?.[e.length - 1]?.prompt;
			if (t && !A(t) && t.content !== "truncated" && t.text) v = t.text, y = "step";
			else {
				y = "registered";
				let e = h?.requests;
				b = t ? A(t) ? { hole: t.badge } : {
					hole: "truncated",
					bytes: t.truncated_bytes
				} : e ? e.badge ? {
					hole: e.badge,
					reason: e.reason,
					fix: e.fix
				} : e.error ? Jn(e.error) : qn() : Kn();
			}
		}
		this.state.drawer = {
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
			await this.openExperiment(e, l());
			return;
		}
		let n = l();
		this.state.drawer = {
			...t,
			verb: n.verb,
			step: n.fromStep,
			thread: n.thread,
			input: n.input
		}, this.emit();
	}
	async runExperiment() {
		let e = this.state.drawer;
		if (!e || this.posting) return;
		let t = bi(e);
		if (t) {
			this.setExperimentError(t, e.under ?? e.runId);
			return;
		}
		this.posting = !0;
		let n = this.loadSeq, r;
		try {
			r = await _r(this.ep, Si(e, this.publicId));
		} catch (t) {
			!this.disposed && n === this.loadSeq && this.setExperimentError(K(t), e.under ?? e.runId);
			return;
		} finally {
			this.posting = !1;
		}
		if (this.disposed || n !== this.loadSeq) return;
		let i = this.state.experiments.get(e.runId)?.length ?? 0, a = this.state.turn, o = a?.children.get(e.runId), s = a && a.id === e.runId ? _i(a.transcript) ?? vi(a.folded) : o ? _i(o.transcript) ?? vi(o.folded) : {
			text: "",
			calls: []
		};
		this.expSub?.close(), this.expSub = void 0;
		let c = Wi(e.runId, yi(e.runId, i), s);
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
					r.error = `the parked run could not be read again — nothing was sent (${K(e)})`, this.emit();
					return;
				}
				if (this.left(r)) return;
				let a = N();
				zi(a, /* @__PURE__ */ new Set(), i.events);
				let o = W(a).pending;
				if (!o.some((t) => t.id === e)) {
					r.folded.pending = o, r.error = `call ${e} is not pending on ${r.runID} — nothing was sent`, this.emit();
					return;
				}
				let s;
				try {
					s = await yr(this.ep, r.runID, {
						call_id: e,
						decision: t,
						content: n
					});
				} catch (e) {
					r.error = K(e), this.emit();
					let t = await this.walkEvents(r.runID).catch(() => null);
					if (t && !this.left(r)) {
						let e = N();
						zi(e, /* @__PURE__ */ new Set(), t.events), r.folded.pending = W(e).pending, this.emit();
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
		let n = _i(await lr(this.ep, e).catch(() => null));
		if (!n) {
			let t = N();
			try {
				zi(t, /* @__PURE__ */ new Set(), (await this.walkEvents(e)).events);
			} catch {}
			n = vi(W(t));
		}
		this.left(t) || (this.compareWords.set(e, n), this.emit());
	}
	compareWords = /* @__PURE__ */ new Map();
	async setBreakpoints(e) {
		let t = this.state.drawer;
		if (!t) return;
		let n = this.loadSeq, r;
		try {
			r = await br(this.ep, t.runtimeId, e);
		} catch (e) {
			!this.disposed && n === this.loadSeq && this.setExperimentError(K(e), t.runId);
			return;
		}
		this.disposed || n !== this.loadSeq || (this.state.breakpoints = Array.isArray(r?.tools) ? r.tools : e, this.emit());
	}
	async steer(e) {
		let t = this.state.result;
		if (t && t.runID && e) {
			try {
				await xr(this.ep, t.runID, e);
			} catch (e) {
				t.error = K(e);
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
			let n = Wi(t ?? this.state.selected, "—", {
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
				r = await vr(this.ep, e.commandID);
			} catch (r) {
				if (this.left(e)) return;
				let i = r instanceof z && [
					401,
					403,
					404,
					410
				].includes(r.status);
				if (i || ++t >= ji) {
					e.state = "lost", e.error = i ? K(r) : "Studio stopped answering — the command's state is unknown", this.emit();
					return;
				}
				this.after(Ai, () => void n().catch(G));
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
				this.after(Ai, () => void n().catch(G));
			}
		};
		n().catch(G);
	}
	followExperiment(e, t) {
		let n = N();
		e.runID = t, e.row = null, e.events = [], e.seen = /* @__PURE__ */ new Set(), e.feed = n, e.folded = W(n), e.pos = -1, e.held = [], e.reading = !1, e.recheck = !1, e.loading = !1, e.stale = !1, e.ready = !1, e.words = null, e.deciding = null, e.decided = {}, this.retries.exp = 0, this.openExperimentStream(e);
	}
	openExperimentStream(e) {
		let t = e.runID, n = () => !this.left(e) && e.runID === t && !e.ready;
		this.expSub?.close(), this.expSub = Tr(this.ep, {
			selector: { run: t },
			kinds: [
				"event",
				"delta",
				"run"
			],
			onOpen: () => {
				this.retries.exp = 0, !this.left(e) && e.runID === t && !e.ready && this.catchUp(e, t, n).catch(G);
			},
			onRecord: (r) => {
				this.state.result !== e || e.runID !== r.run_id || e.ready || this.liveInto(e, t, r, n);
			},
			onRun: (t) => {
				this.state.result === e && t.run.id === e.runID && (e.row = t.run, this.emit(), t.run.status !== "running" && this.settleExperiment(e, 0).catch(G));
			},
			onOverflow: (n) => {
				if (this.expSub = void 0, this.state.result !== e || e.runID !== t || e.ready) return;
				let r = async () => {
					this.state.result !== e || e.runID !== t || e.ready || (await this.loadExperiment(e), !(this.left(e) || e.runID !== t || Gi(e)) && (e.state === "queued" || e.state === "accepted") && this.openExperimentStream(e));
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
				lr(this.ep, t).catch(() => null),
				sr(this.ep, t).catch(() => null)
			]);
		} finally {
			e.loading = !1;
		}
		if (this.disposed || this.state.result !== e || e.runID !== t) return null;
		n && n.events.length && Bi(e, n.events);
		let a = () => !this.left(e) && e.runID === t && !e.ready;
		return this.drain(e, t, a), e.recheck && (e.recheck = !1, this.catchUp(e, t, a).catch(G)), i && (e.row = i), e.folded = Hi(W(e.feed), r, !!e.row && e.row.status !== "running"), e.stale = !1, this.emit(), { transcript: r };
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
		if (!n || Gi(e)) return;
		let r = e.row;
		if ((!r || r.status === "running" || r.status === "succeeded" && !e.folded.finished) && t < 14) {
			this.after(1e3, () => void this.settleExperiment(e, t + 1).catch(G));
			return;
		}
		e.ready = !0, e.words = _i(n.transcript) ?? vi(e.folded), this.expSub?.close(), this.expSub = void 0, this.emit(), this.loadStepDiff(e).catch(G);
	}
	async loadStepDiff(e) {
		if (e.runID && e.sourceRunID && (this.state.meta?.capabilities.includes("diff") ?? !1)) {
			try {
				let t = await ar(this.ep, e.sourceRunID, e.runID);
				if (this.left(e)) return;
				e.stepDiff = t;
			} catch (t) {
				if (this.left(e)) return;
				e.stepDiffError = K(t);
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
			let r = await or(this.ep, {
				public_id: this.publicId,
				limit: "50",
				before: t.before,
				...t.id ? { before_id: t.id } : {}
			});
			if (n !== this.loadSeq || this.disposed || !Oi(this.cursor, t)) return;
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
				t?.stale && (t.stale = !1, t.folded = W(t.feed)), this.notify(this.state);
			});
		}
	}
	dispose() {
		this.disposed = !0, this.raf && cancelAnimationFrame(this.raf), this.raf = 0, this.clearTimers(), this.scopeSub?.close(), this.runSub?.close(), this.expSub?.close(), this.devSub?.close(), this.scopeSub = this.runSub = this.expSub = this.devSub = void 0;
	}
};
function Wi(e, t, n) {
	let r = N();
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
		folded: W(r),
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
function Gi(e) {
	return e.ready;
}
function K(e) {
	return e instanceof Error ? e.message : String(e);
}
function Ki(e, t) {
	return b({
		publicId: e,
		session: t
	});
}
function qi(e) {
	let t = {};
	return e.session && (t.session = e.session), e.flow && (t.flow = e.flow), e.run && (t.run = e.run), t;
}
//#endregion
//#region src/panel/tree.ts
var Ji = 2048, Yi = 200;
function Xi(e) {
	e.timer !== void 0 && e.applied !== e.q && (e.applied = e.q, e.open.clear()), clearTimeout(e.timer), clearTimeout(e.saidTimer), e.timer = e.saidTimer = void 0, e.said === "copied" && (e.said = "");
}
var Zi = () => ({
	open: /* @__PURE__ */ new Map(),
	more: /* @__PURE__ */ new Map(),
	full: /* @__PURE__ */ new Set(),
	q: "",
	applied: "",
	said: "",
	box: "",
	bytes: /* @__PURE__ */ new Map()
}), Qi = (e) => typeof e == "object" && !!e, $i = (e) => Array.isArray(e) ? e.length : Object.keys(e).length;
function ea(e, t, n = Infinity) {
	if (Array.isArray(e)) for (let r = 0; r < Math.min(e.length, n); r++) t(String(r), e[r], r);
	else {
		let r = Object.keys(e);
		for (let i = 0; i < Math.min(r.length, n); i++) t(r[i], e[r[i]], i);
	}
}
var ta = (e, t) => `${e}/${t.replace(/~/g, "~0").replace(/\//g, "~1")}`;
function na(e, t = 2) {
	try {
		return Tn(e, t) ?? String(e);
	} catch {
		return String(e);
	}
}
function ra(e) {
	let t = /* @__PURE__ */ new Set();
	if (!Qi(e)) return t;
	let n = 0, r = [["", e]];
	for (let [e, i] of r) {
		let a = Math.min($i(i), 200);
		e && n + a > 150 || (t.add(e), n += a, ea(i, (t, n) => Qi(n) && r.push([ta(e, t), n]), 200));
	}
	return t;
}
function ia(e, t) {
	let n = /* @__PURE__ */ new Set(), r = /* @__PURE__ */ new Set(), i = 0, a = t.toLowerCase(), o = [[
		"",
		null,
		e
	]];
	for (; o.length;) {
		let [e, t, s] = o.pop(), c = Qi(s) ? "" : String(s);
		if ((t !== null && t.toLowerCase().includes(a) || c.toLowerCase().includes(a)) && (i++, n.size < Yi)) {
			n.add(e);
			for (let t = e.lastIndexOf("/"); t > 0; t = e.lastIndexOf("/", t - 1)) r.add(e.slice(0, t));
			r.add("");
		}
		Qi(s) && ea(s, (t, n) => o.push([
			ta(e, t),
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
function aa(e, t = 0) {
	let n = 0;
	for (let r = t; r < e.length; r++) {
		let t = e.charCodeAt(r);
		t < 128 ? n += 1 : t < 2048 ? n += 2 : (t & 64512) == 55296 && (e.charCodeAt(r + 1) & 64512) == 56320 ? (n += 4, r++) : n += 3;
	}
	return n;
}
var oa = (e) => (e.charCodeAt(2047) & 64512) == 55296 ? 2047 : Ji, sa = (e) => Array.isArray(e) ? `[…] ${e.length} items` : `{…} ${$i(e)} keys`;
function ca(e, t, n) {
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
function la(e, t, n, r) {
	let i = t.applied.trim(), a = null;
	if (i) {
		let n = t.memo;
		a = n && n.root === e && n.q === i ? n : t.memo = {
			root: e,
			q: i,
			...ia(e, i)
		};
	}
	let o = t.auto;
	(!o || o.root !== e) && (o = t.auto = {
		root: e,
		open: ra(e)
	});
	let s = o.open, c = (e) => () => {
		e(), t.said = "", r.redraw();
	}, l = (e) => t.open.get(e) ?? (a?.via.has(e) || s.has(e)), u = [], d = (e, n, i, o) => {
		let s = Qi(n), d = s && l(i), p = I("div", `weft-tn${a?.hits.has(i) ? " weft-hit" : ""}`, void 0, { "data-key": `n${i}` });
		if (p.style.paddingLeft = `${o * 12}px`, s) {
			let n = I("button", "weft-tt", d ? "▾" : "▸", {
				type: "button",
				"aria-expanded": String(d),
				"aria-label": `${d ? "collapse" : "expand"} ${e}`
			});
			L(n, "click", c(() => t.open.set(i, !d))), p.appendChild(n);
		} else p.appendChild(I("span", "weft-tt", "", { "aria-hidden": "true" }));
		if (p.appendChild(I("span", "weft-tk", `${e}: `)), s) p.appendChild(I("span", "weft-tv", sa(n)));
		else if (typeof n == "string" && n.length > 2048 && !t.full.has(i)) {
			let e = oa(n);
			p.appendChild(I("span", "weft-tv", `${JSON.stringify(n.slice(0, e)).slice(0, -1)}…`));
			let r = t.bytes.get(i);
			r?.v !== n && t.bytes.set(i, r = {
				v: n,
				n: aa(n, e)
			});
			let a = I("button", "weft-btn weft-tmore", `… +${r.n} bytes`, {
				type: "button",
				title: "show the whole string"
			});
			L(a, "click", c(() => t.full.add(i))), p.appendChild(a);
		} else p.appendChild(I("span", "weft-tv", na(n, 0)));
		let m = I("button", "weft-tc", "⧉", {
			type: "button",
			title: "copy this node's JSON",
			"aria-label": `copy ${e}`
		});
		L(m, "click", () => ca(t, na(n), r)), p.appendChild(m), u.push(p), d && f(n, i, o + 1);
	}, f = (e, n, r) => {
		let i = 200 + (t.more.get(n) ?? 0), o = 0;
		if (ea(e, (e, t, s) => {
			let c = ta(n, e);
			s < i || a?.via.has(c) || a?.hits.has(c) ? d(e, t, c, r) : o++;
		}), o) {
			let e = I("button", "weft-btn weft-tmore", `… +${o} more`, {
				type: "button",
				"data-key": `m${n}`
			});
			e.style.marginLeft = `${r * 12 + 16}px`, L(e, "click", c(() => t.more.set(n, (t.more.get(n) ?? 0) + 200))), u.push(e);
		}
	};
	Qi(e) ? f(e, "", 0) : d("value", e, "", 0);
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
	L(g, "click", () => ca(t, na(e), r)), p.appendChild(g);
	let _ = I("a", "weft-btn weft-dl", "download", {
		href: "#",
		download: `${n}.json`,
		title: `save as ${n}.json`
	});
	L(_, "click", (t, n) => {
		let r = na(e);
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
var ua = "changed by PrepareStep", da = "overridden by experiment", fa = "catalog changed at this step", pa = "prompt changed at this step", ma = "weft.override.";
function ha(e, t) {
	let n = e?.find((e) => e.attrs["gen_ai.operation.name"] === "invoke_agent" && e.attrs["weft.run.id"] === t);
	if (!n) return;
	let r = [], i;
	for (let [e, t] of Object.entries(n.attrs)) {
		if (!e.startsWith(ma)) continue;
		let n = e.slice(14);
		n === "hash" ? i = typeof t == "string" ? t : String(t) : r.push(n);
	}
	if (!i && r.length === 0) return;
	let a = n.attrs[`${ma}instructions`];
	return {
		hash: i,
		instructions: a === !0 || a === "true",
		fields: r.sort()
	};
}
function ga(e, t, n) {
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
async function _a(e) {
	let t = globalThis.crypto?.subtle;
	if (!t) return;
	let n = await t.digest("SHA-256", new TextEncoder().encode(e));
	return Array.from(new Uint8Array(n), (e) => e.toString(16).padStart(2, "0")).join("");
}
function va(e, t) {
	let n = e;
	for (let e of t) e && (n.length > 0 && (n += "\n\n"), n += e);
	return n;
}
function ya(e, t) {
	if (!t) return;
	let n = new Map(t.tools.map((e) => [e.name, e.prompt_snippet ?? ""]));
	if (!e.some((e) => !n.has(e))) return e.map((e) => n.get(e) ?? "");
}
function ba(e) {
	let t = e?.prompt;
	return !e || !t || A(t) ? e && !e.system_hash ? "" : void 0 : t.content === "derived" ? void 0 : t.text;
}
async function xa(e, t, n) {
	let r = e.system_hash;
	if (r === t || r === "" && t === "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") return !0;
	let i = ba(e);
	if (i === void 0) return;
	if (n === void 0) {
		if (e.body.tools.names.length > 0) {
			let e = await _a(i);
			return e === void 0 || e !== t ? void 0 : !0;
		}
		n = [];
	}
	let a = n.filter(Boolean);
	if (a.length === 0) {
		let e = await _a(i);
		return e === void 0 ? void 0 : e === t;
	}
	let o = a.join("\n\n");
	if (t === "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") return i === o;
	if (!i.endsWith(`\n\n${o}`)) return !1;
	let s = await _a(i.slice(0, i.length - o.length - 2));
	return s === void 0 ? void 0 : s === t;
}
function Sa(e) {
	return e.kind === "previous" ? `diff vs step ${e.step}` : e.verified ? e.overridden ? "diff vs the registered instructions (overridden for this run)" : "diff vs the registered instructions" : "diff vs weft.json's instructions — not verified for this run";
}
function Ca(e, t, n) {
	let r = new Set(e), i = new Set(t), a = [...e.filter((e) => !i.has(e)), ...t.filter((e) => !r.has(e))];
	if (a.length === 0) {
		if (!(e.length !== t.length || e.some((e, n) => e !== t[n]))) return !1;
		if (!n?.verified) return !0;
		let r = ya(t, n.agent);
		return r === void 0 || r.some(Boolean);
	}
	if (!n?.verified) return !0;
	let o = ya(a, n.agent);
	return o === void 0 || o.some(Boolean);
}
function wa(e) {
	let t = [...e].sort((e, t) => e.step - t.step || e.index - t.index), n = /* @__PURE__ */ new Map(), r;
	for (let e of t) r && r.step !== e.step && !n.has(e.step) && n.set(e.step, r), (!r || r.step !== e.step || e.index > r.index) && (r = e);
	return n;
}
function Ta(e, t, n = [], r = 3, i = !1) {
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
function Ea(e) {
	return e < 1024 ? `${e} B` : e < 1048576 ? `${(e / 1024).toFixed(1)} KB` : `${(e / 1048576).toFixed(1)} MB`;
}
//#endregion
//#region src/panel/request.ts
var Da = 12, q = (e, t) => I("div", "weft-rq-hole", [R(e, t)], { "data-weft-rq-hole": e }), Oa = (e, t) => q(e, e === "not_recorded" ? { label: Lt } : e === "stripped" ? t.t.requests?.stripped : void 0), ka = (e, t) => I("div", "weft-rq-hole", Nn(e, t), { "data-weft-rq-hole": e }), J = (e, t, n) => I("div", "weft-rq-row", [I("span", "weft-rq-k", e), I("div", "weft-rq-v", t)], {
	"data-key": n,
	"data-weft-rq": n
}), Y = (e, t = "weft-res") => I("span", t, e), Aa = (e, t) => I("span", "weft-badge weft-info", t, { "data-weft-mark": e });
function ja(e, t, n, r) {
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
var Ma = /* @__PURE__ */ new Map();
function Na(e, t) {
	Ma.set(e, t), Ma.size > 64 && Ma.delete(Ma.keys().next().value);
}
function Pa(e, t, n, r) {
	let i = [
		e.system_hash,
		t,
		e.body.tools.names.join(","),
		n?.join("\0") ?? ""
	].join("|"), a = Ma.get(i);
	if (a !== "pending") {
		if (a !== void 0) return a ?? void 0;
		Na(i, "pending"), xa(e, t, n).then((e) => {
			Na(i, e ?? null), r();
		}, () => Na(i, null));
	}
}
function Fa(e) {
	let t = e?.prompt;
	return !!t && !A(t) && t.truncated_bytes > 0;
}
function Ia(e, t, n, r, i) {
	let a = e.t.doc, o = n === void 0, s = a?.instructions_hash, c = ha(e.t.spans ?? void 0, e.t.id), l = o && c?.instructions === !0, u = o && !!s && i.system_hash !== s, d = !o && (t.steps.get(r)?.promptChanged ?? !1), f = i.body.tools.names, p = n?.body.tools.names ?? [], m = d && (f.length !== p.length || f.some((e, t) => e !== p[t])), h = u || l || m, g = h ? e.manifest() : void 0, _ = !h || g !== void 0, v = a?.agent ? ga(g ?? void 0, a.agent, a.manifest_hash || void 0) : void 0, y = ya(f, v?.agent), b = v?.verified ? y : void 0, x = ba(i);
	if (!o) {
		let e = ba(n);
		return {
			prompt: d ? m && (!_ || Ca(p, f, v)) ? "either" : "prepare_step" : void 0,
			experiment: !1,
			noManifest: !1,
			base: d && e !== void 0 && x !== void 0 ? {
				kind: "previous",
				step: n.step,
				text: e,
				truncated: Fa(n)
			} : void 0
		};
	}
	let ee = u && _ ? Pa(i, s, b, e.redraw) : void 0, S = v && v.agent.instructions !== void 0 && y ? va(v.agent.instructions, y) : void 0;
	return {
		prompt: ee === !1 ? "prepare_step" : void 0,
		experiment: l,
		noManifest: (u || l) && g === null,
		base: S !== void 0 && x !== void 0 && S !== x ? {
			kind: "registered",
			text: S,
			verified: v?.verified,
			overridden: l
		} : void 0
	};
}
var La = /* @__PURE__ */ new WeakMap();
function Ra(e, t, n) {
	let r = I("div", "weft-diff", [I("div", "weft-diff-h", Sa(t))], { "data-weft-prompt-diff": t.kind });
	if (Fa(e) || t.truncated) return r.appendChild(q("truncated", { label: Ht })), r;
	let i = La.get(e);
	(!i || i.base !== t.text) && La.set(e, i = {
		base: t.text,
		diff: kr(t.text, n)
	});
	let a = i.diff;
	if ("tooLarge" in a) return r.appendChild(I("div", "weft-reason", `too large to diff (${a.tooLarge.before} → ${a.tooLarge.after} lines)`, { "data-weft-diff-too-large": "" })), r;
	for (let e of a.rows) r.appendChild(I("div", `weft-diff-row weft-diff-${e.kind}`, `${za[e.kind]}${e.text}`, { "data-weft-diff": e.kind }));
	return r;
}
var za = {
	add: "+ ",
	del: "− ",
	same: "  "
};
function Ba(e, t, n) {
	let r = e.keys.has(t), i = I("button", "weft-btn weft-rq-more", n(r), {
		type: "button",
		"aria-expanded": String(r)
	});
	return L(i, "click", () => {
		e.keys.has(t) ? e.keys.delete(t) : e.keys.add(t), e.redraw();
	}), [i, r];
}
function Va(e, t, n, r) {
	let i = t.prompt;
	if (!t.system_hash) return [Y("no system prompt")];
	if (!i || A(i)) return [Y(j(t.system_hash), "weft-rq-hash"), ...i ? [Oa(i.badge, e)] : []];
	if (i.content === "derived") return [Y(j(i.hash), "weft-rq-hash"), q("derived")];
	let a = i.text.split("\n"), o = a.length > Da, [s, c] = o ? Ba(e, `${r}\u0000rq-prompt`, (e) => e ? "show less" : `show all ${a.length} lines`) : [null, !0], l = [I("div", "weft-res weft-rq-prompt", o && !c ? `${a.slice(0, Da).join("\n")}\n…` : i.text, { "data-weft-prompt": "" })], u = I("div", "weft-holes", [Y(j(i.hash), "weft-rq-hash")]);
	return s && u.appendChild(s), l.push(u), i.truncated_bytes > 0 && l.push(q("truncated", { bytes: i.truncated_bytes })), n.base && n.base.text !== i.text && l.push(Ra(t, n.base, i.text)), l;
}
function Ha(e) {
	let t = I("div", "weft-holes");
	for (let n of [
		`timeout ${e.timeout_ms > 0 ? `${e.timeout_ms}ms` : "none"}`,
		`approval ${e.approval ? "required" : "no"}`,
		`replay ${e.replay || "never"}`,
		`result cap ${e.max_result_bytes > 0 ? Ea(e.max_result_bytes) : "off"}`,
		e.sequential ? "sequential" : "",
		`source ${e.source || "?"}`
	]) n && t.appendChild(I("span", "weft-chip", n));
	return t;
}
function Ua(e, t, n, r) {
	let i = t.tools, a = t.body.tools.names;
	if (!t.catalog_hash) return [Y("no tools offered")];
	if (!i || A(i)) {
		let n = [];
		return a.length && n.push(Y(a.join(", "))), n.push(i ? Oa(i.badge, e) : Y(j(t.catalog_hash), "weft-rq-hash")), n;
	}
	if (i.content === "derived") return [Y(j(i.hash), "weft-rq-hash"), q("derived")];
	let o = I("div", "weft-rq-tools");
	for (let t of i.tools) {
		let [i, a] = Ba(e, `${n}\u0000rq-tool\u0000${t.name}`, (e) => `${e ? "▾" : "▸"} ${t.name}`);
		i.className = "weft-btn weft-rq-tool";
		let s = I("div", void 0, [i], {
			"data-key": `tool:${t.name}`,
			"data-weft-tool": t.name
		});
		a && (s.appendChild(I("div", "weft-res", t.description)), s.appendChild(Ha(t)), s.appendChild(la(t.schema, e.tree(`schema\u0000${r}\u0000${t.name}`), `${t.name}-schema`, e.cx))), o.appendChild(s);
	}
	let s = [o, I("div", "weft-holes", [Y(j(i.hash), "weft-rq-hash")])];
	return i.truncated_bytes > 0 && s.push(q("truncated", { bytes: i.truncated_bytes })), s;
}
var Wa = /* @__PURE__ */ new WeakMap();
function Ga(e, t, n) {
	let r = Wr(e.t.doc), i = Wa.get(t);
	(!i || i.tr !== e.t.transcript || i.comps !== e.t.doc || i.running !== e.running) && Wa.set(t, i = {
		tr: e.t.transcript,
		comps: e.t.doc,
		running: e.running,
		m: Ta(t, e.t.transcript, r, void 0, e.running)
	});
	let a = i.m, o = `${a.count} ${a.count === 1 ? "message" : "messages"}`, s = [I("div", "weft-res", a.bytes === void 0 ? o : `${o} · ${Ea(a.bytes)}`, { "data-weft-messages-line": "" })];
	if (a.hole === "no_transcript" ? s.push(I("div", "weft-reason", "bytes when the transcript is read")) : a.hole && s.push(a.hole === "compacted" || a.hole === "no_index" && t.content === "stripped" ? q(a.hole === "compacted" ? a.hole : "stripped") : q("gap", { reason: a.hole === "gap" ? Bt : Vt })), a.last.length) {
		let e = I("div", void 0, void 0, { "data-weft-messages-last": "" });
		for (let t of a.last) e.appendChild(I("div", "weft-res", Yr(t)));
		s.push(e);
	}
	if (a.before.length) {
		let [t, r] = Ba(e, `${e.t.id}\u0000rq-earlier\u0000${n}`, (e) => `${e ? "▾" : "▸"} ${a.before.length} earlier ${a.before.length === 1 ? "message" : "messages"} (raw)`);
		s.push(t), r && s.push(I("div", void 0, [la(a.before, e.tree(`earlier\u0000${n}`), `step${n}-messages`, e.cx)], { "data-weft-messages-earlier": "" }));
	}
	return s;
}
function Ka(e, t) {
	let n = new Set(e.folded.steps.map((e) => e.index));
	for (let e of t?.steps.keys() ?? []) n.add(e);
	return [...n].sort((e, t) => e - t);
}
function qa(e) {
	let t = e.t, n = t.requests, r = I("div", "weft-rq");
	if (n?.badge) return r.appendChild(ka(n.badge, {
		reason: n.reason,
		fix: n.fix
	})), r;
	if (!n) return r.appendChild(t.doc?.requests_badge ? ka(t.doc.requests_badge) : ka("not_recorded", {
		cause: "not_served",
		reason: e.notServed
	})), r;
	if (n.error) return r.appendChild(I("span", "weft-badge weft-err", `request could not be read: ${n.error}`)), r;
	let i = Ka(t, n), a = e.step !== void 0 && i.includes(e.step) ? e.step : i[0] ?? 0;
	r.appendChild(ja("step", i, a, e.select));
	let o = I("div", "weft-rq-pane", void 0, {
		"data-key": `rq${a}`,
		"data-weft-rq-pane": String(a)
	});
	r.appendChild(o);
	let s = Wr(t.doc);
	for (let t of s) (U(t) ? a === i[0] : t.step === a) && o.appendChild(e.compaction(t));
	let c = n.steps.get(a), l = c?.rows ?? [];
	if (!l.length) return o.appendChild(e.running ? I("div", "weft-reason", Ut) : n.truncated ? Pn(10 * ur) : q("gap", { reason: Rt })), r;
	let u = wa([...n.steps.values()].flatMap((e) => e.rows)), d = l[0], f = e.pick.get(a), p = l.find((e) => e.attempt === f) ?? l[l.length - 1], m = Ia(e, n, u.get(a), a, d), h = I("div", "weft-holes weft-rq-head", void 0, { "data-key": "head" }), g = [p.body.model.provider, p.body.model.name].filter(Boolean).join("/");
	g && h.appendChild(Y(g));
	let _ = t.folded.steps.find((e) => e.index === a), v = Qr(Zr(l, !!_?.finish || (_?.toolCalls.length ?? 0) > 0, e.running));
	v && h.appendChild(I("span", "weft-badge weft-info", v, { "data-weft-attempts": "" })), m.prompt && h.appendChild(Aa("prompt", m.prompt === "either" ? pa : ua)), m.experiment && h.appendChild(Aa("experiment", da)), c?.catalogChanged && h.appendChild(Aa("catalog", fa)), o.appendChild(h), l.length > 1 && o.appendChild(ja("attempt", l.map((e) => e.attempt), p.attempt, (t) => {
		e.pick.set(a, t), e.redraw();
	})), p.content && o.appendChild(q(p.content, p.content === "stripped" ? n.stripped : void 0)), m.noManifest && o.appendChild(I("div", "weft-reason", "no manifest readable here: no diff vs the registered instructions", { "data-weft-no-manifest": "" }));
	let y = `${t.id}\u0000${a}`, b = p.body, x = m.base && p.system_hash === d.system_hash ? m : {
		...m,
		base: void 0
	};
	o.appendChild(J("system", Va(e, p, x, y), "system")), o.appendChild(J("messages", Ga(e, p, a), "messages")), o.appendChild(J("tools", Ua(e, p, y, a), "tools"));
	let ee = I("div", "weft-rq-params");
	for (let [e, t] of Ft(p)) ee.appendChild(I("div", void 0, [I("span", "weft-rq-pk", e), I("span", t === "adapter default" ? "weft-rq-def" : "weft-res", t)], { "data-weft-param": e }));
	o.appendChild(J("params", [ee], "params")), o.appendChild(J("tool choice", [Y(b.tool_choice ? `${b.tool_choice.mode}${b.tool_choice.name ? ` (${b.tool_choice.name})` : ""}` : "adapter default")], "tool_choice")), o.appendChild(J("thinking", [Y(b.thinking ? `${b.thinking.level}${b.thinking.budget ? ` · budget ${b.thinking.budget}` : ""}` : "adapter default")], "thinking"));
	let S = _?.toolCalls.filter((e) => e.childRunId) ?? [];
	return S.length && o.appendChild(J("subagents", S.map((t) => e.child(t)), "subagents")), r;
}
//#endregion
//#region src/panel/element.ts
function Ja(e, t) {
	return e.meta?.capabilities.includes(t) ?? !1;
}
function Ya(e) {
	return e.status === "succeeded" && e.pending > 0 ? "parked" : e.status;
}
function Xa(e, t, n) {
	return V(e, jr(t, n === void 0 ? {} : {
		step: n,
		view: "story"
	}));
}
var Za = null;
function Qa(e) {
	try {
		if ("adoptedStyleSheets" in e && typeof CSSStyleSheet == "function") {
			if (!Za) {
				let e = new CSSStyleSheet();
				e.replaceSync(pi), Za = e;
			}
			e.adoptedStyleSheets = [Za];
			return;
		}
	} catch {}
	e.append(I("style", void 0, pi));
}
var X = () => {}, $a = 3e3;
function Z(e) {
	return b({
		publicId: e.publicId,
		session: e.session,
		flow: e.flow
	});
}
function eo(e, t, n) {
	e.add(t), e.size > n && e.delete(e.values().next().value);
}
function to(e, t, n, r) {
	e.delete(t), e.set(t, n), e.size > r && e.delete(e.keys().next().value);
}
var no = /* @__PURE__ */ new Set(), ro = [
	"data-scope=\"pub_…\" on the panel's <script> tag (or <weft-devtools>)",
	"scope(\"pub_…\") from @weftgo/devtools",
	"data-weft-scope=\"pub_…\" on the chat's element",
	"scope.Header(h, …) on the app's handler (Go, package weft/scope)"
], io = "no conversation detected on this page";
function ao() {
	try {
		return typeof window.matchMedia == "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
	} catch {
		return !1;
	}
}
function oo(e) {
	let t = (typeof e.composedPath == "function" ? e.composedPath()[0] : null) ?? e.target;
	return !!t && (/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) || t.isContentEditable);
}
var so = [
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
], co = [
	"run",
	"parked",
	"error"
], lo = /* @__PURE__ */ new WeakSet(), uo = null;
function fo(e) {
	if (!e || typeof e != "object") return !1;
	let t = Object.getPrototypeOf(e);
	return t === Object.prototype || t === null;
}
var po = 1e3, mo = 3;
function ho(e) {
	if (!e || typeof e != "object") return null;
	let t = e, n = (...e) => {
		for (let n of e) if (typeof t[n] == "string" && t[n]) return t[n];
		return "";
	}, r = n("session", "sessionId");
	if (typeof t.publicId != "string" && !r) return null;
	let i = { publicId: typeof t.publicId == "string" ? t.publicId : "" }, a = n("flow"), o = n("run", "runId");
	return r && (i.session = r), a && (i.flow = a), o && (i.run = o), i;
}
var go = 1e4;
function _o(e) {
	let t = e.scope;
	return typeof t == "string" ? t : t ? b(t) : e.publicId ? b({ publicId: e.publicId }) : "";
}
var vo = (e) => typeof e == "number" && Number.isInteger(e) && e >= 0 ? e : void 0, yo = class e extends HTMLElement {
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
	tree = Zi();
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
	last = Fi();
	held = !1;
	composing = !1;
	dirty = !1;
	holdTimer = null;
	scratch = /* @__PURE__ */ new Map();
	openKeys = /* @__PURE__ */ new Set();
	onKey = (e) => this.keydown(e);
	onURL = () => {
		let e = Qe();
		(e ? b(e) : "") !== (this.cfg.urlScope ? b(this.cfg.urlScope) : "") && this.rescan();
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
		super(), this.cfg = Ke(this), this.lay = ce(this.cfg.position, this.cfg.open, this.cfg.mode), this.shadow = this.attachShadow({ mode: "open" }), this.body = I("div", "weft-root"), Qa(this.shadow), this.shadow.append(this.body), this.shadow.addEventListener("pointerdown", () => this.hold()), this.shadow.addEventListener("keydown", (e) => this.panelKey(e)), this.shadow.addEventListener("compositionstart", () => {
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
		ge(this.lay), no.add(this), window.addEventListener("keydown", this.onKey), window.addEventListener("resize", this.onResize, { passive: !0 }), window.addEventListener("pointerup", this.onRelease, !0), window.addEventListener("pointercancel", this.onRelease, !0), window.addEventListener("hashchange", this.onURL, { passive: !0 }), window.addEventListener("popstate", this.onURL, { passive: !0 }), this.themeWatch.start(() => {
			this.getAttribute("data-theme-resolved") !== this.resolveNow() && this.render(this.last);
		}), this.themeNow = this.resolveNow(), this.syncTheme(), this.syncGlobal(), this.schedule();
	}
	disconnectedCallback() {
		no.delete(this), window.removeEventListener("keydown", this.onKey), window.removeEventListener("resize", this.onResize), this.resizeFrame !== null && (typeof cancelAnimationFrame == "function" && cancelAnimationFrame(this.resizeFrame), clearTimeout(this.resizeFrame), this.resizeFrame = null), Xi(this.tree);
		for (let e of this.rqTrees.values()) Xi(e);
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
				let a = _o(i);
				a ? this.setAttribute("data-weft-scope", a) : this.removeAttribute("data-weft-scope"), this.rescan(), this.render(this.last);
				return;
			}
			let n = typeof e == "string" ? x(e) : ho(e);
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
		let t = b(e), n = this.options?.scope;
		if ((typeof n == "string" ? n : n ? b(n) : "") === t && this.getAttribute("data-weft-scope") === t) {
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
				let e = await hr({
					base: this.base,
					token: this.cfg.token
				}, n);
				i = typeof e?.public_id == "string" ? e.public_id : "", a = typeof e?.badge == "string" ? e.badge : "", o = {
					reason: typeof e?.reason == "string" ? e.reason : void 0,
					fix: typeof e?.fix == "string" ? e.fix : void 0
				};
			} catch (e) {
				if (t !== this.scopeSeq) return;
				let r = e instanceof z ? e.status : 0;
				this.say(r === 403 || r === 401 ? `session ${n}: the session lookup needs the dev token` : r === 404 ? `session ${n} has no public id · unknown session` : `session ${n}: Studio did not answer the lookup`);
				return;
			}
			if (t === this.scopeSeq) {
				if (!i) {
					this.say(`session ${n} has no public id · ${a === "not_recorded" ? In(o.reason, o.fix) : "none recorded"}`);
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
			let r = vo(t), i = ++this.selectSeq;
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
		if (!co.includes(e) || typeof t != "function") return () => {};
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
			return Xa(n, e, vo(t));
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
		return lo.add(t), this.apiObj = t, t;
	}
	syncGlobal() {
		try {
			if (!(this.isConnected && this.cfg.global && !e.noGlobal)) {
				this.globalNote = "", this.dropGlobal();
				return;
			}
			let t = window, n = t.weft;
			if (n === void 0) uo = {}, n = uo, t.weft = n;
			else if (!fo(n)) {
				this.globalNote = "window.weft is the page's: no window.weft.devtools";
				return;
			}
			let r = n, i = Object.getOwnPropertyDescriptor(r, "devtools");
			if (i ? !i.writable && !i.set : !Object.isExtensible(r)) {
				this.globalNote = "window.weft is the page's: no window.weft.devtools";
				return;
			}
			let a = r.devtools, o = a !== void 0 && !!a && typeof a == "object" && lo.has(a);
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
			if (!fo(t) || t.devtools !== this.apiObj || !this.apiObj) return;
			delete t.devtools, Array.from(document.querySelectorAll("weft-devtools")).find((e) => e !== this && e.isConnected && typeof e.syncGlobal == "function")?.syncGlobal(), !("devtools" in t) && t === uo && Object.keys(t).length === 0 && (delete e.weft, uo = null);
		} catch {}
	}
	say(e) {
		this.note = e, this.render(this.last);
	}
	afterSettle(e) {
		this.settled().then(e).catch(X);
	}
	async settled() {
		let e = Date.now() + go;
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
		let n = Ki(t.publicId, t.narrowing.session);
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
			let a = Ya(n), o = this.evStatus.get(n.id);
			if (o === a || (this.evStatus.set(n.id, a), o === void 0 && r && a !== "running" && n.id !== i)) continue;
			let s = (e.turn?.id === n.id ? e.turn.folded.steps.at(-1)?.index : void 0) ?? (n.steps > 0 ? n.steps - 1 : void 0);
			this.fire("run", {
				runId: n.id,
				status: a,
				publicId: n.public_id || t.publicId,
				...n.session_id ? { sessionId: n.session_id } : {},
				...s === void 0 ? {} : { step: s }
			}), a === "failed" && !this.evErrored.has(n.id) && (eo(this.evErrored, n.id, 500), this.fire("error", {
				message: n.err || `run ${n.id} failed`,
				runId: n.id
			})), a === "parked" && this.reportParked(t, n.id, 1);
		}
	}
	reportParked(e, t, n) {
		this.evParking.has(t) || (this.evParking.add(t), e.pendingCalls(t).then((r) => {
			this.evParking.delete(t);
			let i = e.rowOf(t);
			if (this.model === e && i && Ya(i) === "parked") {
				if (!r.length) {
					n < mo && setTimeout(() => {
						this.model === e && this.reportParked(e, t, n + 1);
					}, po);
					return;
				}
				for (let e of r) {
					let n = `${t}\u0000${e.id}`;
					this.evParked.has(n) || (eo(this.evParked, n, 500), this.fire("parked", {
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
		return !!this.cfg.urlScope && !this.cfg.scopeExplicit && b(this.scopeNow()) === b(this.cfg.urlScope);
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
		return [b(this.scopeNow()), ...this.conversations().map((e) => e.key + e.source)].join("|");
	}
	onMarkers(e) {
		let t = this.markers, n = (e) => b(e.scope);
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
		let e = this.markers, t = (t) => t ? e.find((e) => e.element === t) : void 0, n = this.marked ? Z(this.marked) : "", r = t(oi(document.activeElement)) ?? t(this.lastFocused) ?? e.find((e) => n && Z(e.scope) === n), i = Z(this.cfg.scope);
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
		t && !this.markerRung ? this.markerRung = li({
			onScopes: (e) => this.onMarkers(e),
			onFocus: (e) => this.onMarkerFocus(e)
		}) : !t && this.markerRung && this.dropMarkers();
		let n = this.cfg.scopeExplicit ? b(this.cfg.scope) : "";
		n !== this.explicitForm && (this.explicitForm = n, this.chosen = this.marked = this.lastFocused = null, this.explicitMarked = !1, n && (this.forceNext = !0), this.follow());
		let r = this.cfg.urlScope ? b(this.cfg.urlScope) : "";
		r !== this.urlForm && (this.urlForm = r, r && !this.cfg.scopeExplicit && (this.chosen = null, this.forceNext = !0));
	}
	dropRung() {
		this.rung && (this.notRestored = !this.rung.restore(), this.rung = null, this.detected = null, this.byPath.clear(), this.seen = [], this.followedPath = "");
	}
	onDetected(e, t) {
		let n = Z(e);
		to(this.byPath, t, e, 64);
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
		let o = a ? b(a) : "";
		this.detected = e, b(e) !== o && !this.cfg.scopeExplicit && this.schedule();
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
		e.defaultPrevented || e.isComposing || oo(e) || this.ownsToggle() && (e.altKey && !e.ctrlKey && !e.shiftKey && !e.metaKey && e.code === "KeyW" || e.ctrlKey && e.shiftKey && !e.altKey && !e.metaKey && e.code === "KeyW") && (e.preventDefault(), this.keys = !1, this.toggle(), this.shown && this.body.querySelector(".weft-dock")?.focus({ preventScroll: !0 }));
	}
	ownsToggle() {
		let e;
		try {
			let t = window.weft, n = fo(t) ? Object.getOwnPropertyDescriptor(t, "devtools")?.value : void 0;
			if (n) for (let t of no) t.apiObjIs(n) && (e = t);
		} catch {}
		let t = (e) => !e.dormant && !e.autoMounted;
		if (e && t(e)) return e === this;
		for (let e of no) if (t(e)) return e === this;
		return (e ?? no.values().next().value) === this;
	}
	panelKey(e) {
		if (e.defaultPrevented || e.isComposing) return;
		if (e.key === "Tab") return this.trap(e);
		if (oo(e)) return;
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
		let t = this.last, n = t.turn ? this.lay.tab === "request" ? Ka(t.turn, t.turn.requests) : t.turn.folded.steps.map((e) => e.index) : [];
		if (!n.length) return;
		let r = n.indexOf(this.stepNow(t) ?? -1), i = r < 0 ? e > 0 ? 0 : n.length - 1 : Math.min(Math.max(r + e, 0), n.length - 1);
		this.model?.selectStep(n[i]);
	}
	stepNow(e) {
		let t = Do(e);
		return t !== void 0 || this.lay.tab !== "request" || !e.turn || e.turn.id !== e.selected ? t : Ka(e.turn, e.turn.requests)[0];
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
			let n = this.scopeNow(), r = b(n);
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
		this.ready = !1, this.dropRung(), this.model?.dispose(), this.model = null, this.conn = null, this.probe?.abort(), this.probe = null, this.scratch.clear(), this.byPath.clear(), this.prompts.clear(), this.evParked.clear(), this.evErrored.clear(), e && this.dormant && this.unreachable !== null ? (this.checking = !0, this.render(Fi())) : (this.dormant = !1, this.unreachable = null, this.checking = !1, this.render(Fi()));
		let r = !1, i = n.endpoint;
		if (n.configURL) {
			let e = new AbortController();
			this.probe = e;
			let r = setTimeout(() => e.abort(), $a);
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
			let a = new Ui({
				base: i,
				token: n.token
			}, e, (e) => this.render(e));
			a.prefer = this.lay.run, this.model = a, this.conn = {
				endpoint: n.endpoint,
				token: n.token,
				scope: b(e)
			}, this.base = i, this.last = a.state;
			try {
				r = await a.start();
			} catch {
				r = !1;
			}
			if (t !== this.startSeq || this.model !== a) return;
		}
		if (this.checking = !1, r) {
			this.ready = !0, this.dormant && (this.dormant = !1, this.unreachable = null), this.syncDetect(), b(this.scopeNow()) !== this.conn?.scope && this.schedule(), this.render(this.last);
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
		w.includes(e) && (this.lay.tab !== "raw" && (this.prevTab = this.lay.tab), this.lay.tab = e, this.lay.raw = e === "raw", this.save(), this.render(this.last));
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
		if (Cn(t, n), r && !this.shadow.activeElement && t.querySelector(".weft-dock, .weft-fab")?.focus({ preventScroll: !0 }), this.focusNext) {
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
			let t = I("option", void 0, `${Ya(r)} · ${r.id} · ${r.steps} steps`, {
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
		let i = e.turn?.id === n.id ? e.turn.folded.steps.length : 0, a = Math.max(i, n.steps), o = `weft devtools · running${a ? `, step ${a}` : ""}`, s = I("button", `${r} weft-fab-running${ao() ? "" : " weft-fab-pulse"}`, [document.createTextNode("devtools"), I("span", "weft-fab-count", a ? ` ● ${a}` : " ●")], {
			title: `${o} — Alt+W`,
			"aria-label": o
		});
		return this.pillEnd(s, e, n);
	}
	pillEnd(e, t, n) {
		let r = n ?? [...t.turns, ...[...t.experiments.values()].flat()].find((e) => e.id === t.selected);
		if (r) {
			let t = dt(r.status);
			e.appendChild(I("span", "weft-fab-cost", ` · ${t ? `${H(r.usage.input_tokens)}→${H(r.usage.output_tokens)}` : "—"}`, { title: t ? "the current turn's tokens, input→output" : "the current turn's usage: known when it finishes" }));
		}
		return n || e.setAttribute("aria-label", `weft ${e.textContent}`), this.cfg.zIndex && (e.style.zIndex = this.cfg.zIndex), L(e, "click", () => this.toggle()), e;
	}
	howToBox() {
		let e = I("div", "weft-howto");
		for (let t of ro) e.appendChild(I("code", void 0, t));
		return e;
	}
	go(e) {
		e?.catch(X);
	}
	shortcuts() {
		let e = I("div", "weft-keys"), t = I("dl");
		for (let [e, n] of so) t.appendChild(I("dt", void 0, e)), t.appendChild(I("dd", void 0, n));
		return e.appendChild(t), e;
	}
	header(e) {
		let t = I("div", "weft-head"), n = e.turns.some((e) => e.status === "running");
		t.appendChild(I("span", `weft-dot${e.live ? n ? " weft-run" : " weft-on" : ""}`, void 0, { title: e.live ? "live" : "history" }));
		let r = e.session?.agent ?? e.turns.at(0)?.agent ?? "", i = this.model?.publicId || e.session?.public_id || "", a = i ? `${r ? r + " · " : ""}${i}` : io;
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
		let c = e.turns.reduce((e, t) => e + t.usage.input_tokens, 0), l = e.turns.reduce((e, t) => e + t.usage.output_tokens, 0), u = `${e.turns.length}${e.turnsCapped ? "+" : ""} turns · ${H(c)}→${H(l)} tok`;
		if (t.appendChild(I("span", void 0, u, { title: u })), e.turns.length && e.selected) {
			let n = I("a", "weft-btn", "⤢", {
				href: Xa(this.base, e.selected, this.stepNow(e)),
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
		if (n && e.sessionUnrecorded && t.appendChild(I("div", "weft-note", `session ${n}: these runs carry no session id — not narrowed`)), !this.model?.publicId && e.devRefused ? t.appendChild(I("div", "weft-note weft-dev-poll", "streaming needs the server token · polling")) : !this.model?.publicId && e.devAgent && t.appendChild(I("div", "weft-note weft-dev-poll", `live: agent ${e.devAgent} · the other agents' runs every ${Ni / 1e3} s`)), !e.turns.length && !e.experiments.size) {
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
		if (t.status && Ya(e) !== t.status || t.err && !e.err && e.status !== "failed") return !1;
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
		let r = Ya(e), i = I("button", `weft-turn${e.id === t ? " weft-sel" : ""}`, void 0, {
			type: "button",
			tabindex: e.id === n ? "0" : "-1",
			...e.id === t ? { "aria-current": "true" } : {}
		}), a = I("div", "weft-row1", [
			I("span", `weft-chip weft-${r}`, r),
			I("span", "weft-id", e.id, { title: e.id }),
			...Bn(e, (e) => {
				let t = this.rowOf(e);
				return t?.turn ? `t${t.turn}` : $(e);
			}),
			I("span", "weft-when", Br(e.last_seen || e.started))
		]), o = e.usage, s = I("div", "weft-row2", [
			I("span", void 0, e.model.name ? `${e.model.provider}/${e.model.name}` : ""),
			I("span", void 0, `${e.steps} steps`),
			I("span", void 0, `${H(o.input_tokens)}→${H(o.output_tokens)}`),
			I("span", void 0, Vr(e.started, e.finished) || "…")
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
				href: V(this.base, jr(t, { step: r })),
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
		}), e.tooNew && e.meta) return t.appendChild(I("div", "weft-note weft-warn", [I("span", "weft-warn", "Studio is newer than this panel; update panel.js"), I("span", void 0, `studio_version ${e.meta.studio_version} · panel built for ${wi()}`)])), t;
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
		return a.appendChild(this.playgroundArea(e)), n !== "story" && a.setAttribute("hidden", ""), i(a, "story"), n !== "story" && i(I("div", void 0, [n === "raw" ? this.rawView(r) : n === "timeline" ? ko(r) : this.requestView(e, r)]), n), t;
	}
	tabs() {
		let e = I("div", "weft-tabs", void 0, {
			role: "tablist",
			"aria-label": "turn views",
			"data-key": "tabs"
		});
		for (let t of w) {
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
			let t = e.key, n = w.indexOf(this.lay.tab), r = w.length, i = t === "ArrowRight" ? n + 1 : t === "ArrowLeft" ? n - 1 : t === "Home" ? 0 : t === "End" ? r - 1 : null;
			if (i === null) return;
			e.preventDefault();
			let a = w[(i + r) % r];
			this.setTab(a), this.body.querySelector(`#weft-tab-${a}`)?.focus();
		}), e;
	}
	playgroundArea(e) {
		let t = I("div"), n = e.turn?.id ?? "";
		return e.result && e.result.under === n && t.appendChild(this.experimentResult(e)), this.canAct(e) && e.drawer && (e.drawer.under ?? e.drawer.runId) === n && t.appendChild(this.drawer(e)), t;
	}
	canAct(e) {
		return g(e.meta?.capabilities ?? [], this.cfg.token);
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
		if (t.verb && s.appendChild(I("div", "weft-verb-t", bo[t.verb], { "data-weft-drawer-verb": t.verb })), t.key !== void 0 && t.key !== this.focusedKey) {
			this.focusedKey = t.key;
			let e = t.edits.at(0);
			this.focusNext = t.focus === "edit" && e ? `edit:${e.step}:${e.callID}` : t.focus === "input" ? "input" : "prompt";
		}
		let c = I("label", "weft-field", [I("span", void 0, "System prompt")]), l = this.field(I("textarea", "weft-input"), "prompt");
		l.rows = 3, l.value = t.instructions, L(l, "input", (e, t) => this.model?.setDraft({ instructions: t.value }, !0));
		let u = I("button", "weft-btn", "↺", {
			title: "reset to the registered prompt",
			"aria-label": "reset to the registered prompt"
		});
		if (L(u, "click", () => {
			this.model?.setDraft({ instructions: t.registeredInstructions });
		}), c.append(l, u), s.appendChild(c), t.promptFrom === "registered") {
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
		let d = I("div", "weft-fields"), f = I("select", "weft-input", void 0, { "aria-label": "model" }), p = e.turn?.doc?.model.name ?? "", m = I("option", void 0, `model: ${p || "—"}`);
		m.value = "", f.appendChild(m);
		for (let e of n?.models ?? []) {
			if (e === p) continue;
			let t = I("option", void 0, e);
			t.value = e, f.appendChild(t);
		}
		let h = t.modelFree ?? (t.model && !(n?.models ?? []).includes(t.model) ? t.model : ""), g = !h.trim();
		if (f.value = g ? t.model : "", t.model && f.classList.add("weft-ovr"), L(f, "change", (e, t) => this.model?.setDraft({
			model: t.value,
			modelFree: ""
		})), d.appendChild(f), n?.resolver) {
			let e = I("input", "weft-input" + (g ? "" : " weft-ovr"), void 0, {
				"aria-label": "model name the app resolves",
				placeholder: "or any model: the app resolves it"
			});
			e.value = h, L(e, "input", (e, t) => {
				let n = t.value;
				this.model?.setDraft({
					model: n.trim(),
					modelFree: n
				});
			}), d.appendChild(e);
		}
		let _ = I("select", "weft-input", void 0, { "aria-label": "thinking" }), v = I("option", void 0, `thinking: default${n?.defaults?.thinking ? ` (${n.defaults.thinking})` : ""}`);
		v.value = "", _.appendChild(v);
		for (let e of [
			"off",
			"low",
			"medium",
			"high"
		]) {
			let t = I("option", void 0, e);
			t.value = e, _.appendChild(t);
		}
		if (_.value = t.thinking, L(_, "change", (e, t) => this.model?.setDraft({ thinking: t.value })), d.appendChild(_), s.appendChild(d), t.step === 0) {
			let e = I("label", "weft-field", [I("span", void 0, "Input (replaces the user message)")]), n = this.field(I("textarea", "weft-input"), "input");
			n.rows = 2, n.value = t.input, t.sourceInput && t.thread !== "fork" && (n.placeholder = t.sourceInput), L(n, "input", (e, t) => {
				let n = t.value;
				this.model?.setDraft({ input: n }, !0);
				let r = t.closest(".weft-drawer")?.querySelector(".weft-run-btn");
				r && (r.disabled = r.hasAttribute("data-weft-held") || this.model?.state.drawer?.thread === "fork" && !n.trim());
			}), e.appendChild(n), s.appendChild(e);
		}
		let y = I("div", "weft-fields"), b = I("select", "weft-input", void 0, { "aria-label": "side effects" });
		b.title = "How side-effect tools behave in the re-run. ReplaySafe tools always run; the others substitute, park, or — under allow, if the app opted them in — run for real.";
		let x = I("option", void 0, "side effects: substitute", { title: "a side-effect call the source recorded is answered from the record; any other call parks for you" });
		x.value = "", b.appendChild(x);
		let ee = I("option", void 0, "park", { title: "every side-effect call parks for you; nothing is answered from the record" });
		ee.value = "park", b.appendChild(ee);
		let S = I("option", void 0, "allow — runs the tools this app opted in (AllowSideEffects) for real", { title: "refused unless every tool left on is opted in or ReplaySafe" });
		S.value = "allow", b.appendChild(S), b.value = t.sideEffects === "substitute" ? "" : t.sideEffects, L(b, "change", (e, t) => this.model?.setDraft({ sideEffects: t.value })), y.appendChild(b);
		let te = I("select", "weft-input", void 0, { "aria-label": "engine" }), C = I("option", void 0, "engine: live");
		C.value = "live", te.appendChild(C);
		let ne = I("option", void 0, "scripted (zero tokens)");
		ne.value = "scripted", te.appendChild(ne), te.value = t.engine, L(te, "change", (e, t) => this.model?.setDraft({ engine: t.value })), y.appendChild(te);
		let re = I("select", "weft-input", void 0, { "aria-label": "thread" }), ie = I("option", void 0, "thread: ephemeral");
		ie.value = "ephemeral", re.appendChild(ie);
		let w = I("option", void 0, "fork (new session)");
		if (w.value = "fork", re.appendChild(w), re.value = t.thread, re.title = "fork continues the conversation in a new session (needs an input)", L(re, "change", (e, t) => {
			let n = t.value;
			this.model?.setDraft(n === "fork" ? {
				thread: n,
				step: 0
			} : { thread: n });
		}), y.appendChild(re), s.appendChild(y), n && s.appendChild(this.lab(t, n)), Ja(e, "breakpoints") && tt(this.cfg.token) === "" && n?.tools.length) {
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
		let ae = 0, T = e.turn ? t.runId === e.turn.id ? e.turn : e.turn.children.get(t.runId) : void 0;
		if (t.step > 0 && T) {
			let e = () => this.model?.state.drawer ?? t, n = I("div", "weft-field");
			n.appendChild(I("span", void 0, `Transcript edits (steps 0..${t.step - 1} are kept)`));
			let r = /* @__PURE__ */ new Set();
			for (let i of T.folded.steps) {
				let a = i.index;
				if (!(a >= t.step)) {
					for (let t of i.toolCalls) {
						if (!t.result) continue;
						let i = I("label", "weft-edit", void 0, { "data-key": `${a}:${t.callId}` });
						i.appendChild(I("span", void 0, `step ${a} · ${t.name} →`));
						let o = this.field(I("input", "weft-input"), `edit:${a}:${t.callId}`);
						o.placeholder = String(t.result.content).slice(0, 60);
						let s = () => e().edits.find((e) => e.step === a && e.callID === t.callId);
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
						let o = () => e().edits.find((e) => e.step === a && !e.callID);
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
			for (let e of t.edits) r.has(`${e.step}:${e.callID ?? ""}`) || (ae++, n.appendChild(I("div", "weft-edit", [R("gap"), document.createTextNode(` step ${e.step} · ${e.callID ?? "reply"} → ${e.toolResult ?? e.content ?? ""} · not in the steps read · Run held`)], {
				"data-key": `u:${e.step}:${e.callID ?? ""}`,
				"data-weft-unplaced": ""
			})));
			n.childElementCount > 1 && s.appendChild(n);
		}
		let oe = this.ack(e, t, T, n, s), se = I("button", "weft-run-btn", "Run experiment ▶", { title: "POST /api/playground/runs — the runtime in your app executes it" }), ce = oe || ae || xi(t).problems.length;
		return ce && se.setAttribute("data-weft-held", ""), (ce || t.thread === "fork" && !t.input.trim()) && se.setAttribute("disabled", ""), L(se, "click", () => this.go(this.model?.runExperiment())), s.appendChild(se), r.appendChild(s), r;
	}
	lab(e, t) {
		let n = e.lab ?? dn(), r = t.defaults, i = hn(t), a = (e) => this.model?.setDraft({ lab: {
			...this.model.state.drawer?.lab ?? dn(),
			...e
		} }), { problems: o } = xi(e), s = I("div", "weft-field weft-lab", [I("span", void 0, "Options · empty keeps the agent's default (greyed)")], { "data-weft-lab": "" });
		i && s.appendChild(I("div", "weft-note", `this runtime reports no defaults — ${mn}`, { "data-weft-lab-old": "" }));
		let c = (e, t) => {
			for (let n of o) n.field === e && t.appendChild(I("div", "weft-lab-p", n.message, {
				role: "alert",
				"data-weft-lab-problem": e
			}));
		};
		c("lab", s);
		let l = (e, t) => {
			let o = I("label", "weft-lab-k", [I("span", void 0, P[e]), t], { "data-key": `lab:${e}` });
			if (i && pn.includes(e) && t.setAttribute("disabled", ""), yn(n, e, r)) {
				t.classList.add("weft-ovr"), t.setAttribute("data-weft-override", "");
				let r = I("button", "weft-btn", "↺", {
					type: "button",
					"aria-label": `reset ${P[e]} to the agent's default`
				});
				L(r, "click", () => a(e === "tool_choice" ? {
					tool_choice: "",
					tool_choice_name: ""
				} : { [e]: Array.isArray(n[e]) ? [] : "" })), o.appendChild(r);
			}
			c(e, o), s.appendChild(o);
		};
		for (let e of [...fn, "stop"]) {
			let t = I(e === "stop" ? "textarea" : "input", "weft-input", void 0, {
				"aria-label": P[e],
				placeholder: F(e, r) || (i ? "" : e === "stop" ? "none" : "adapter default")
			});
			t.value = n[e], L(t, "input", (t, n) => a({ [e]: n.value })), l(e, t);
		}
		let u = I("select", "weft-input", void 0, { "aria-label": P.tool_choice }), d = r?.tool_choice;
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
				"aria-label": P[e],
				"data-key": `lab:${e}`
			});
			t.appendChild(I("span", void 0, `${P[e]} · default ${e === "park_on" ? "none" : "every tool"}`));
			for (let r of f) {
				let o = I("input", void 0, void 0, { "aria-label": `${P[e]}: ${r}` });
				o.type = "checkbox", o.checked = n[e].includes(r), o.disabled = i || e === "only_tools" && p(r) && !o.checked, L(o, "change", (t, n) => {
					let i = this.model?.state.drawer?.lab?.[e] ?? [];
					a({ [e]: n.checked ? [...i, r] : i.filter((e) => e !== r) });
				}), t.appendChild(I("span", "weft-tool" + (o.checked ? " weft-ovr" : ""), [o, I("span", void 0, r)]));
			}
			if (yn(n, e, r)) {
				let n = I("button", "weft-btn", "↺", {
					type: "button",
					"aria-label": `reset ${P[e]} to the agent's default`
				});
				L(n, "click", () => a({ [e]: [] })), t.appendChild(n);
			}
			c(e, t), s.appendChild(t);
		}
		return s;
	}
	closeDrawer() {
		let e = this.opener;
		this.opener = null, this.model?.closeExperiment(), e?.isConnected && e.focus({ preventScroll: !0 });
	}
	ack(e, t, a, o, s) {
		let c = I("div", "weft-ack", void 0, {
			"data-weft-ack": "",
			"data-key": "ack"
		});
		c.appendChild(I("div", "weft-name", "before you run"));
		let l = t.thread === "fork" ? 0 : t.step, u = l > 0 && Wr(a?.doc).some((e) => !U(e) && typeof e.step == "number" && e.step <= l);
		c.appendChild(I("div", "weft-res", m(l, u), { "data-weft-prefix": "" }));
		let d = a && "status" in a ? a.status : this.rowOf(t.runId)?.status ?? a?.doc?.status ?? "", f = a ? xo(a.requests, l, Ja(e, "requests"), So(a.transcript)?.stepCount ?? null, d === "running") : {
			tools: null,
			hole: Vn("the run's records could not be read")
		};
		f.hole && c.appendChild(Hn(f.hole, !!o));
		let p = Object.keys(t.tools), h = xi(t).overrides, g = h.only_tools ?? p.filter((e) => t.tools[e]), _ = n({
			catalog: f.tools ?? (o?.tools ?? []).map((e) => ({
				name: e.name,
				replay: e.side_effects === "safe" ? "safe" : "never",
				approval: !1
			})),
			agent: o,
			mode: t.sideEffects,
			toolsEnabled: g.length < p.length ? g : void 0,
			breakpoints: [...i({ breakpoints: e.breakpoints }, o), ...h.park_on ?? []]
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
			r ? `${H(r.input_tokens)}→${H(r.output_tokens)} tok` : "",
			t.row ? Vr(t.row.started, t.row.finished) : ""
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
			href: V(this.base, Pr(t.runID ? { run: t.runID } : {})),
			target: "_blank",
			rel: "noopener",
			title: "hand off to Studio: the run's records as wefttest replay fixtures (D4)"
		});
		s.style.textDecoration = "none", a.appendChild(s);
		let c = I("a", "weft-btn", "compare in Studio", { title: "open the Studio playground with this run, step and the current overrides carried over" }), l = (n) => {
			let r = this.model?.state.drawer ?? null, i = r && r.runId === t.sourceRunID ? r : null, a = i ? i.step : e.turn ? Oo(e.turn.folded, e.selectedStep) : -1;
			n.setAttribute("href", Bo(this.base, i, a));
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
			n.appendChild(i("⤢", V(this.base, jr(e, { step: t.fromStep })), `open the source step in Studio (run ${e} step ${t.fromStep})`, "data-weft-source-link")), n.appendChild(i("open the replayed run ⤢", V(this.base, jr(t.runID)), `open the replayed run ${t.runID} in Studio`, "data-weft-run-link")), d.appendChild(n);
		}
		let f = t.row?.status ?? (t.ready ? "succeeded" : "running");
		for (let e of t.folded.steps) {
			e.text && d.appendChild(I("div", void 0, e.text));
			for (let n of e.toolCalls) d.appendChild(Io(n, e.index, f, void 0, void 0, t.runID ? {
				endpoint: this.base,
				runId: t.runID
			} : void 0));
		}
		!t.folded.steps.length && !t.error && t.state === "queued" ? d.appendChild(I("div", "weft-note", "queued — waiting for the runtime to ack…")) : !t.folded.steps.length && !t.error && t.state === "accepted" && !t.runID && d.appendChild(I("div", "weft-note", "accepted — the fork's turn is running in its new session…"));
		let p = [{
			id: "",
			label: Vo(t.label)
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
		let m = t.ready ? t.words ?? vi(t.folded) : null, h = t.compareWith ? this.model?.compareWords.get(t.compareWith) : t.source, g = t.compareWith ? $(t.compareWith) : Vo(t.label);
		if (m && h && m.text && h.text) {
			let e = I("div", "weft-diff");
			this.diffInto(e, `diff vs ${g}:`, h.text, m.text), h.calls.join("\n") !== m.calls.join("\n") && this.diffInto(e, "tool calls:", h.calls.join("\n"), m.calls.join("\n")), d.appendChild(e);
		}
		if (t.ready && Ja(e, "diff") && (t.stepDiff ? d.appendChild(zr(t.stepDiff, this.base)) : t.stepDiffError ? d.appendChild(I("div", "weft-note weft-warn", `the step compare could not be read: ${t.stepDiffError}`)) : t.runID && t.sourceRunID && d.appendChild(I("div", "weft-note", "reading the step compare…", { "data-weft-step-diff-loading": "" }))), t.ready && t.folded.pending.length && t.runID && this.canAct(e) && d.appendChild(this.decisions(t.folded.pending, t.decided)), Ja(e, "steer") && this.canAct(e) && t.state === "accepted" && t.runID) {
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
		let i = Er(n, r);
		e.appendChild(I("div", "weft-diff-h", `${t}  ${Dr(i)}`));
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
			let e = I("div", "weft-call", void 0, { "data-key": n.id }), i = I("div", "weft-call-h", [I("span", "weft-name", n.name), I("span", "weft-args", n.args === void 0 ? "(…)" : En(n.args))]);
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
		let i = gi(t.transcript);
		i && to(this.prompts, t.id, i, 500), i && n.appendChild(I("div", "weft-note", i));
		let a = Wr(t.doc);
		for (let e of a.filter(U)) n.appendChild(No(e, a, t.transcript, {
			keys: this.openKeys,
			scope: t.id
		}));
		let o = this.rowOf(t.id);
		return n.appendChild(jo(t.folded, o?.status ?? t.doc?.status ?? "running", t, e.selectedStep, {
			keys: this.openKeys,
			scope: t.id
		}, {
			endpoint: this.base,
			runId: t.id,
			...this.canAct(e) ? { replay: {
				runId: t.id,
				agent: o?.agent,
				max: So(t.transcript)?.max ?? null,
				fork: !!(o?.session_id || t.doc?.session_id) && !(o?.parent_run_id || t.doc?.parent_run_id),
				open: (e, n, r, i) => {
					this.opener = i, this.go(this.model?.openExperiment(e, n, {
						agent: r,
						under: t.id
					}));
				}
			} } : {}
		})), t.folded.pending.length && n.appendChild(this.approvals(t.folded.pending, !!o?.playground)), this.canAct(e) && n.appendChild(this.actions(e)), n;
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
		return n && i.appendChild(a(`session ${n}`, V(this.base, Mr(n)), "the session in Studio", "data-weft-session-link", n)), r && i.appendChild(a(`trace ${r.slice(0, 8)}`, V(this.base, Nr(r)), `the OTel trace ${r} in Studio`, "data-weft-trace-link", r)), i;
	}
	actions(e) {
		let t = e.turn;
		if (!t) return I("div");
		let n = I("div", "weft-actions"), r = I("button", "weft-btn", "✎ Experiment", { title: "open the experiment drawer, pre-filled from the registered config" });
		L(r, "click", () => this.go(this.model?.openExperiment(t.id, 0))), n.appendChild(r);
		let i = I("button", "weft-btn", "↻ Re-run", { title: "re-run the whole turn: the drawer from step 0, with its current edits — Run after the ack" });
		L(i, "click", () => this.go(this.model?.rerun(t.id))), n.appendChild(i);
		let a = Oo(t.folded, e.selectedStep);
		if (a > 0) {
			let e = I("button", "weft-btn", `⎇ Continue from step ${a}`, { title: "keep the transcript through the previous step (edits apply) and run this step fresh" });
			L(e, "click", () => this.go(this.model?.openExperiment(t.id, o(a)))), n.appendChild(e);
		}
		return n;
	}
	notes(e) {
		let t = I("div");
		t.setAttribute("data-weft-turn-holes", "");
		let n = Eo(e, this.rowOf(e.id));
		for (let e of n) t.appendChild(jn(e));
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
				I("span", "weft-args", n.args === void 0 ? "(…)" : En(n.args)),
				I("span", "weft-badge weft-info", "parked")
			])), e.appendChild(I("div", "weft-res", t ? "an experiment's run — its decision controls are in the result pane of the turn that ran it" : "the app's own turns are viewer-only (PQ7) — decide from your app")), r.appendChild(e);
		}
		return n.appendChild(r), n;
	}
	footer(e) {
		let t = [I("span", void 0, "prompts, args and results from your app, via your Studio")], n = Rn(e.turn, e.meta, 1e4);
		n && t.push(I("span", "weft-cap", [document.createTextNode(` · ${n.text}`), I("span", "weft-sr", ` — ${n.title}`)], {
			title: n.title,
			"data-weft-cap": n.hole ?? ""
		}));
		let r = this.detectWord();
		return t.push(I("span", "weft-detect", ` · detect: ${r}${this.rung?.chained ? " (chained)" : ""}${this.notRestored && !this.rung ? " (fetch not restored: patched after the panel)" : ""}`, { title: r.startsWith("url") ? "scope from the page URL's weft_scope" : r.includes("headers") || r === "markers" ? "reading Weft-Scope on same-origin fetches / data-weft-scope markers" : "scope from data-scope / window.__WEFT__" })), this.globalNote && t.push(I("span", "weft-global", ` · global: ${this.globalNote}`)), I("div", "weft-footer", t, { "data-key": "foot" });
	}
	rawView(e) {
		this.treeFor !== e.id && (this.tree = {
			...Zi(),
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
		}), la(this.rawMemo.doc, this.tree, e.id.replace(/[\\/]/g, "_"), {
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
		return qa({
			t,
			step: this.stepNow(e),
			running: r === "running",
			manifest: () => this.readManifest(n, t.doc?.manifest_hash),
			notServed: e.meta?.capabilities_off?.requests,
			keys: this.openKeys,
			tree: (e) => {
				let t = this.rqTrees.get(e);
				return t || this.rqTrees.set(e, t = Zi()), t;
			},
			cx: {
				redraw: n,
				root: () => this.shadow
			},
			select: (e) => this.model?.selectStep(e),
			pick: this.rqPick,
			redraw: n,
			compaction: (e) => No(e, Wr(t.doc), t.transcript, i),
			child: (n) => {
				let r = n.childRunId ?? "", a = t.children.get(r), o = I("div", "weft-call", [I("div", "weft-call-h", [I("span", "weft-name", n.name, { title: r }), I("span", "weft-args", "subagent · step 0")])], {
					"data-key": `sub:${n.callId}`,
					"data-weft-rq-child": r
				});
				return a ? o.appendChild(a.requests ? Fo(0, a.requests, a.status, i) : I("div", "weft-req", Nn("not_recorded", {
					cause: "not_served",
					reason: e.meta?.capabilities_off?.requests
				}))) : t.unreachable.has(r) ? o.appendChild(I("div", "weft-reason", [I("span", void 0, "child's history unreachable · "), I("a", void 0, "open in Studio (⤢)", {
					href: Xa(this.base, r),
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
			B({
				base: this.base,
				token: this.cfg.token
			}, "manifest").then((e) => n(Array.isArray(e?.agents) ? e : null), () => n(null));
		}
		return r.doc;
	}
	rowOf(e) {
		return this.model?.rowOf(e);
	}
}, bo = {
	from_step: "Replay from this step",
	edit_result: "Edit this result and replay",
	edit_prompt: "Edit the prompt and replay",
	rerun: "Re-run",
	continue: "Continue here with a new message"
};
function xo(e, t, n, r = null, i = !1) {
	if (!e) return n ? {
		tools: null,
		loading: !0
	} : {
		tools: null,
		hole: Vn("the server has no request route")
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
		hole: Vn(e.error)
	};
	let a = e.steps.get(t)?.rows, o = a?.[a.length - 1];
	if (!o) return e.truncated && t > Po(e) ? {
		tools: null,
		hole: Wn(10 * ur)
	} : r !== null && t >= r ? {
		tools: null,
		hole: Un()
	} : i ? {
		tools: null,
		hole: Gn()
	} : {
		tools: null,
		hole: {
			hole: "gap",
			reason: Rt
		}
	};
	let s = o.tools;
	return s ? A(s) ? {
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
function So(e) {
	return e ? un(e.batches) : null;
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
var Co = (e, t) => I("span", "weft-verbs", t, {
	"data-weft-verbs": e,
	"data-key": `verbs:${e}`
});
function wo(e, t) {
	let n = [
		Q(`replay from this step (step ${e})`, "↦", o(e), t),
		Q(`edit the prompt and replay (step ${e})`, "✎", c(e), t),
		Q("re-run the whole turn", "↻", l(), t)
	];
	return t.fork && n.push(Q("continue here with a new message", "⎇", u(), t)), Co("step", n);
}
function To(e, t, n) {
	let r = [];
	return t.result && t.callId && n.max !== null && e + 1 <= n.max && r.push(Q(`edit this result and replay (call ${t.callId})`, "✎", s(e, t.callId, String(t.result.content)), n)), r.push(Q(`replay from this step (call ${t.callId}, step ${e})`, "↦", o(e), n)), Co("call", r);
}
function Eo(e, t) {
	return D(nn(e.doc, e.folded), ut({
		status: t?.status ?? e.doc?.status,
		stop_reason: t?.stop_reason ?? e.doc?.stop_reason,
		gaps: e.gaps
	}));
}
function Do(e) {
	if (e.selectedStep != null) return e.selectedStep;
	let t = e.turn;
	if (t && t.id === e.selected) return ([...e.turns, ...[...e.experiments.values()].flat()].find((e) => e.id === t.id)?.status ?? t.doc?.status) === "running" ? t.folded.steps.at(-1)?.index : void 0;
}
function Oo(e, t) {
	return t != null && e.steps.some((e) => e.index === t) ? t : -1;
}
function ko(e) {
	let t = e.spans ?? [], n = Dn(t), r = On(t), i = "time", a = 0;
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
	])), o.appendChild(Ao(r)), o;
}
function Ao(e) {
	let t = I("div", "weft-wf");
	for (let n of e) {
		let e = I("div", "weft-wf-row");
		e.appendChild(I("span", "weft-wf-name", n.name, { title: n.name }));
		let r = I("span", "weft-wf-track"), i = I("span", "weft-wf-bar");
		i.style.left = `${(n.left * 100).toFixed(2)}%`, i.style.width = `${(n.width * 100).toFixed(2)}%`, r.appendChild(i), e.appendChild(r), e.appendChild(I("span", "weft-wf-ms", n.label ?? `${n.ms}ms`)), t.appendChild(e);
	}
	return t;
}
function jo(e, t, n, r, i, a) {
	let o = I("div");
	e.model?.name && o.appendChild(I("div", "weft-reason", `${e.model.provider}/${e.model.name}`));
	let s = t === "running" && !a?.child ? e.steps.at(-1) : void 0;
	for (let c of e.steps) o.appendChild(Mo(c, t, n, r, i, a, c === s));
	return o;
}
function Mo(e, t, n, r, i, a, s = !1) {
	let c = I("div", "weft-step", void 0, { "data-key": `s${e.index}` });
	c.setAttribute("data-weft-step", String(e.index)), r === e.index && (c.style.outline = "1px solid var(--weft-accent)");
	let l = I("div", "weft-step-h", [I("span", void 0, `step ${e.index}`), I("span", "weft-grow")]), u = a?.child ? a.child.requests : n?.requests, d = u?.steps.get(e.index)?.rows ?? [], f = !u?.error && (!u?.truncated || e.index < Po(u));
	e.finish && (l.appendChild(I("span", void 0, e.finish.reason)), l.appendChild(I("span", void 0, Uo(e.finish.usage))));
	let p = f ? Qr(Zr(d, !!e.finish || e.toolCalls.length > 0, t === "running")) : null;
	if (p && l.appendChild(I("span", "weft-badge weft-info", p, { "data-weft-attempts": "" })), e.finish) {
		let t = ei(e.finish.latencyMs, e.finish.ttftMs, "ttft");
		t && l.appendChild(I("span", void 0, t, { "data-weft-timing": "" }));
	}
	let m = tn(e, a?.child ? a.child.doc?.holes : n?.doc?.holes), h = f ? ti(e.finish, d.length) : null, g = An(h ? D(m, [h]) : m);
	g && l.appendChild(g), a?.replay && l.appendChild(wo(e.index, a.replay)), c.appendChild(l);
	let _ = I("div", "weft-step-b"), v = Wr(a?.child ? a.child.doc : n?.doc);
	for (let t of v) !U(t) && t.step === e.index && _.appendChild(No(t, v, a?.child ? a.child.transcript : n?.transcript, i));
	if (u && _.appendChild(Fo(e.index, u, t, i)), e.reasoning) {
		let t = I("details", "weft-collapsible");
		if (i) {
			let n = `${i.scope}\u0000reasoning\u0000${e.index}`;
			t.setAttribute("data-weft-open", n), i.keys.has(n) && t.setAttribute("open", "");
		}
		t.appendChild(I("summary", void 0, "reasoning")), t.appendChild(I("div", void 0, e.reasoning)), _.appendChild(t);
	}
	if (s ? _.appendChild(I("div", "weft-stream", e.text, {
		"aria-live": "polite",
		"aria-busy": "true"
	})) : e.text && _.appendChild(I("div", void 0, e.text)), e.steer) {
		let t = I("div", "weft-note", `steered: ${e.steer.text}`, { "data-key": "steer" }), n = e.index + 1, r = a?.replay?.max;
		a?.replay && r != null && n <= r && t.appendChild(Co("steer", [Q(`replay from this steer (step ${n} runs fresh)`, "↦", o(n), a.replay)])), _.appendChild(t);
	}
	for (let r of e.toolCalls) _.appendChild(Io(r, e.index, t, n, i, a));
	return c.appendChild(_), c;
}
function No(e, t, n, r) {
	let i = U(e), a = I("div", "weft-note");
	a.setAttribute("data-weft-compaction", i ? "session" : String(e.step ?? ""));
	let o = I("div", "weft-call-h", [I("span", "weft-name", i ? Kr : "compaction"), I("span", "weft-args", Gr(e))]), s = An([{ hole: "compacted" }]);
	s && o.appendChild(s), a.appendChild(o);
	let c = I("details", "weft-collapsible");
	if (r) {
		let t = `${r.scope}\u0000compaction\u0000${i ? `session\u0000${e.hash}` : `view\u0000${e.index ?? ""}`}`;
		c.setAttribute("data-weft-open", t), r.keys.has(t) && c.setAttribute("open", "");
	}
	if (c.appendChild(I("summary", void 0, "show original")), i) c.appendChild(I("div", "weft-res", qr(e)));
	else {
		let r = Jr(e, n, t);
		if ("loading" in r) c.appendChild(I("div", "weft-res", "loading the transcript…"));
		else if ("gap" in r) {
			let e = An([{
				hole: "gap",
				reason: r.gap
			}]);
			e && c.appendChild(e), c.appendChild(I("div", "weft-reason", r.gap));
		} else if (!r.messages.length) c.appendChild(I("div", "weft-res", `nothing replaced: inserted at message ${r.from}`));
		else for (let [e, t] of r.messages.entries()) c.appendChild(I("div", "weft-res", Yr(t), { "data-weft-original": String(r.from + e) }));
		c.appendChild(I("div", "weft-reason", Xr(e)));
	}
	return a.appendChild(c), a;
}
function Po(e) {
	let t = -1;
	for (let n of e.steps.keys()) n > t && (t = n);
	return t;
}
function Fo(e, t, n, r) {
	let i = I("div", "weft-req");
	if (i.setAttribute("data-weft-request", String(e)), t.badge) return i.append(...Nn(t.badge, {
		reason: t.reason,
		fix: t.fix
	})), i;
	if (t.error) return i.appendChild(I("span", "weft-badge weft-err", `request could not be read: ${t.error}`)), i;
	let a = t.steps.get(e), o = a?.rows[a.rows.length - 1];
	if (!a || !o) return i.appendChild(n !== "running" && t.truncated ? Pn(10 * ur) : n === "running" ? I("span", "weft-res", `request: ${Ut}`) : I("span", void 0, Nn("gap", { reason: Rt }))), i;
	let s = I("div", "weft-call-h", [I("span", "weft-name", "request"), I("span", "weft-args", a.rows.map((e) => `attempt ${e.attempt}`).join(" · "))]);
	a.promptChanged && s.appendChild(I("span", "weft-badge weft-info", "prompt changed at this step")), a.catalogChanged && s.appendChild(I("span", "weft-badge weft-info", "catalog changed at this step")), o.content && o.content !== "stripped" && s.appendChild(R(o.content)), i.appendChild(s), o.content === "stripped" && i.append(...Nn("stripped"));
	let c = o.prompt;
	if (c && !A(c)) {
		let t = I("details", "weft-collapsible");
		if (r) {
			let n = `${r.scope}\u0000request\u0000${e}`;
			t.setAttribute("data-weft-open", n), r.keys.has(n) && t.setAttribute("open", "");
		}
		let n = c.text;
		t.appendChild(I("summary", void 0, `system prompt · ${n.length} chars`)), t.appendChild(I("div", "weft-res", n)), i.appendChild(t);
	} else if (o.system_hash) {
		let e = I("div", "weft-res", `system prompt ${j(o.system_hash)}`);
		c && e.append(" · ", R(c.badge)), i.appendChild(e);
	}
	let l = o.body.tools.names;
	return i.appendChild(I("div", "weft-res", `tools: ${l.length ? l.join(", ") : "none"}`)), i.appendChild(I("div", "weft-res", `params: ${It(o)}`)), i;
}
function Io(e, t, n, r, i, a) {
	let o = I("div", "weft-call", void 0, { "data-key": e.callId }), s = rn(e, n), c = a?.runId, u = I("div", "weft-call-h", [a?.endpoint && c ? I("a", "weft-name", e.name, {
		href: V(a.endpoint, jr(c, {
			step: t,
			call: e.callId,
			resumed: e.resumed
		})),
		target: "_blank",
		rel: "noopener",
		title: `open this call in Studio (step ${t})`,
		"data-weft-call-link": e.callId
	}) : I("span", "weft-name", e.name), I("span", "weft-args", Ho(e))]);
	o.appendChild(u);
	let d = a?.replay;
	if (e.childRunId && (r || a?.child)) {
		if (u.appendChild(a?.endpoint ? I("a", "weft-badge weft-info", "subagent", {
			href: Xa(a.endpoint, e.childRunId),
			target: "_blank",
			rel: "noopener",
			title: e.childRunId,
			"data-weft-subagent-link": e.childRunId
		}) : I("span", "weft-badge weft-info", "subagent", { title: e.childRunId })), a?.child) a.endpoint && u.appendChild(zo(a.endpoint, e.childRunId));
		else if (r) {
			if (d) {
				let t = r.doc?.children.find((t) => t.id === e.childRunId)?.agent ?? "";
				u.appendChild(Co("child", [Q(`replay the child run ${e.childRunId} (agent ${t || "unnamed"})`, "↻", l(), {
					...d,
					runId: e.childRunId,
					agent: t,
					fork: !1
				})]));
			}
			o.appendChild(Ro(e.childRunId, r, i, a?.endpoint, d));
		}
	}
	if (d && u.appendChild(To(t, e, d)), e.result) {
		let t = Lo(r, e);
		t && u.appendChild(I("span", "weft-badge weft-info", t));
		let n = cn(String(e.result.content));
		n && u.appendChild(Fn(n)), e.result.isError && u.appendChild(I("span", "weft-badge weft-err", "error"));
		let i = An(e.holes ?? []);
		i && u.appendChild(i), o.appendChild(I("div", "weft-res", e.result.content));
	} else s === "running" ? o.appendChild(I("div", "weft-res", "running…")) : o.appendChild(I("div", "weft-res weft-warn", "never completed"));
	return o;
}
function Lo(e, t) {
	if (!e?.spans) return "";
	let n = e.spans.filter((e) => e.name === "execute_tool"), r = n.find((e) => e.attrs["gen_ai.tool.call.id"] === t.callId) ?? n.find((e) => e.attrs["gen_ai.tool.call.id"] === void 0 && e.attrs["gen_ai.tool.name"] === t.name);
	if (!r) return "";
	let i = Date.parse(r.end) - Date.parse(r.start);
	return !Number.isFinite(i) || i < 0 ? "" : `${Math.round(i)}ms`;
}
function Ro(e, t, n, r, i) {
	let a = t.children.get(e), o = t.doc?.children.find((t) => t.id === e), s = I("details", "weft-collapsible");
	s.setAttribute("data-weft-child", e), t.expanded.has(e) && s.setAttribute("open", "");
	let c = I("summary", void 0, o ? `subagent ${o.agent || $(e)} · ${o.status} · ${dt(o.status) ? Uo(o.usage) : o.status === "running" ? ft : "—"}` : `subagent ${$(e)}`);
	c.setAttribute("aria-expanded", String(t.expanded.has(e)));
	let l = o && An(a?.doc?.holes ?? lt(o));
	if (l && c.appendChild(l), s.appendChild(c), r && s.appendChild(zo(r, e)), !a) s.appendChild(I("div", void 0, "loading the subagent's turn…"));
	else {
		let t = o?.status ?? "succeeded";
		s.appendChild(jo(a.folded, t, void 0, void 0, n && {
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
				max: So(a.transcript)?.max ?? null,
				fork: !1
			} } : {}
		})), a.capped && s.appendChild(I("div", "weft-note weft-warn", "a long run: its first events are shown"));
	}
	return s;
}
function zo(e, t) {
	return I("a", "weft-btn", "open in Studio ⤢", {
		href: Xa(e, t),
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
function Bo(e, t, n) {
	let r = {};
	if (t) {
		t.runId && (r.run = t.runId), n != null && n > 0 && (r.step = n), t.instructions && t.instructions !== t.registeredInstructions && (r.instructions = t.instructions);
		let e = Object.entries(t.tools).filter(([, e]) => e).map(([e]) => e);
		e.length && e.length < Object.keys(t.tools).length && (r.tools = e.join(",")), t.model && (r.model = t.model), t.thinking && (r.thinking = t.thinking), t.input && t.step === 0 && (r.input = t.input), t.engine === "scripted" && (r.engine = t.engine), t.sideEffects && t.sideEffects !== "substitute" && (r.side_effects = t.sideEffects), t.thread === "fork" && (r.thread = t.thread), t.agent && (r.agent = t.agent), t.runtimeId && (r.runtime = t.runtimeId);
	}
	return V(e, Pr(r));
}
function Vo(e) {
	return e.split("·")[0] || e;
}
function Ho(e) {
	if (e.args !== void 0) try {
		return `(${JSON.stringify(e.args)})`;
	} catch {
		return "(?)";
	}
	return e.streamedArgs ? `(${e.streamedArgs}…)` : "(…)";
}
function Uo(e) {
	let t = [`${H(e.input_tokens)}→${H(e.output_tokens)} tok`];
	return e.cached_input_tokens && t.push(`${H(e.cached_input_tokens)} cached`), e.reasoning_tokens && t.push(`${H(e.reasoning_tokens)} reasoning`), e.cache_write_tokens && t.push(`${H(e.cache_write_tokens)} cache-write`), t.join(" · ");
}
//#endregion
//#region src/panel/main.ts
function Wo() {
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
function Go() {
	let e = document.createElement("weft-devtools");
	e.autoMounted = !0, document.body?.appendChild(e);
}
function Ko() {
	if (customElements.get("weft-devtools") || customElements.define("weft-devtools", yo), document.readyState === "loading") {
		document.addEventListener("DOMContentLoaded", () => {
			try {
				qo();
			} catch {}
		}, { once: !0 });
		return;
	}
	qo();
}
function qo() {
	Wo();
	let e = Ke();
	document.querySelector("weft-devtools") || (e.auto || nt()) && Go();
}
try {
	Ko();
} catch {}
//#endregion
