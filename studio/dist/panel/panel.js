//#region src/lib/scope.ts
var e = [
	"session",
	"flow",
	"run"
], t = (e) => {
	try {
		return encodeURIComponent(e);
	} catch {
		return e.replace(/[%;=\x00-\x1f\x7f]/g, (e) => `%${e.charCodeAt(0).toString(16).toUpperCase().padStart(2, "0")}`);
	}
}, n = (e) => {
	try {
		return decodeURIComponent(e);
	} catch {
		return e;
	}
};
function r(n) {
	let r = [t(n.publicId)];
	for (let i of e) {
		let e = n[i];
		e && r.push(`${i}=${t(e)}`);
	}
	return r.join(";");
}
function i(t) {
	let [r = "", ...i] = String(t).split(";"), a = { publicId: n(r.trim()) };
	for (let t of i) {
		let r = t.indexOf("=");
		if (r < 0) continue;
		let i = t.slice(0, r).trim();
		if (!e.includes(i)) continue;
		let o = i, s = n(t.slice(r + 1).trim());
		s && a[o] === void 0 && (a[o] = s);
	}
	return a;
}
//#endregion
//#region src/panel/detect.ts
var a = "Weft-Scope";
function o(e) {
	try {
		return typeof e == "function" && Function.prototype.toString.call(e).includes("[native code]");
	} catch {
		return !1;
	}
}
function s(e) {
	try {
		if (typeof e == "string") return e;
		if (e instanceof URL) return e.href;
		let t = e?.url;
		return typeof t == "string" ? t : "";
	} catch {
		return "";
	}
}
function c(e) {
	try {
		let t = new URL(e).hostname.toLowerCase();
		return t === "localhost" || t.endsWith(".localhost") || /^127(\.\d{1,3}){3}$/.test(t) || t === "[::1]";
	} catch {
		return !1;
	}
}
function l(e, t) {
	try {
		if (new URL(e).origin === new URL(t).origin) return !0;
	} catch {
		return !1;
	}
	return c(e) && c(t);
}
function u(e) {
	let t;
	try {
		t = window.fetch;
	} catch {
		return null;
	}
	if (typeof t != "function") return null;
	let n = (e.isNativeFetch ?? o)(t), r = !0, c = (t, n) => {
		if (!r || !t || typeof t != "object") return;
		let o = t.headers;
		if (!o || typeof o.get != "function") return;
		let c = o.get(a);
		if (!c) return;
		let u = t.url, d = e.pageURL ? e.pageURL() : location.href, f = new URL(typeof u == "string" && u ? u : s(n), d);
		if (!l(f.href, d) || e.ignore?.(f.href)) return;
		let p = i(c);
		(p.publicId || p.session || p.flow || p.run) && e.onScope(p, f.pathname);
	}, u = function(...e) {
		let n = t.apply(this, e);
		return !r || !n || typeof n.then != "function" ? n : n.then((t) => {
			try {
				c(t, e[0]);
			} catch {}
			return t;
		});
	}, d = () => {
		r = !1;
		try {
			window.fetch !== t && (window.fetch = t);
		} catch {}
		return null;
	};
	try {
		if (window.fetch = u, window.fetch !== u) return d();
	} catch {
		return d();
	}
	return {
		chained: !n,
		restore() {
			r = !1;
			try {
				return window.fetch === u && (window.fetch = t, !0);
			} catch {
				return !1;
			}
		}
	};
}
//#endregion
//#region src/panel/layout.ts
var d = [
	"float",
	"right",
	"bottom",
	"left",
	"top"
], f = [
	"story",
	"request",
	"timeline",
	"raw"
], p = "weft.devtools", m = (e, t, n) => Math.min(Math.max(e, t), Math.max(t, n)), h = () => window.innerWidth || 1024, g = () => window.innerHeight || 768;
function _(e, t, n) {
	let r = e.endsWith("-dock");
	return {
		mode: n === "float" ? "float" : r || n === "dock" ? "dock" : "float",
		side: r ? e.slice(0, -5) : "right",
		open: n === "pill" ? !1 : n === "float" || n === "dock" || t,
		hidden: n === "hidden",
		x: e === "bottom-left" ? 16 : h() - 520 - 16,
		y: g() - 560 - 16,
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
function v() {
	try {
		return window.localStorage;
	} catch {
		return null;
	}
}
function y() {
	try {
		let e = JSON.parse(v()?.getItem("weft.devtools") ?? "null");
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
		].includes(e.side) && (t.side = e.side), f.includes(t.tab) || (t.tab = t.raw ? "raw" : "story"), t;
	} catch {
		return {};
	}
}
var b = [
	"mode",
	"side",
	"open",
	"hidden",
	"x",
	"y",
	"w",
	"h",
	"d"
], x = (e) => b.some((t) => Object.hasOwn(e, t));
function S(e, t) {
	try {
		let n = { v: 1 };
		for (let [r, i] of Object.entries(e)) (t || !b.includes(r)) && (n[r] = i);
		v()?.setItem(p, JSON.stringify(n));
	} catch {}
}
function C() {
	let e = !1;
	try {
		let t = v();
		e = t?.getItem("weft_debug") === "1", e && (t?.setItem(p, JSON.stringify({
			v: 1,
			...y(),
			debug: !0
		})), t?.removeItem("weft_debug"));
	} catch {}
	return e || y().debug === !0;
}
var w = (e) => e === "left" || e === "right";
function T(e) {
	let t = h(), n = g();
	e.w = m(e.w, Math.min(360, t - 16), t - 16), e.h = m(e.h, Math.min(280, n - 16), n - 16), e.x = m(e.x, 0, t - e.w), e.y = m(e.y, 0, n - e.h);
	let r = w(e.side) ? t : n, i = w(e.side) ? 360 : 280;
	e.d = m(e.d, Math.min(i, r - 16), r - 16);
}
function ee(e) {
	let t = h();
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
	let n = w(e.side);
	return {
		cls: `weft-docked weft-side-${e.side}`,
		style: n ? { width: `${e.d}px` } : { height: `${e.d}px` },
		width: n ? e.d : t,
		sheet: !1
	};
}
function te(e) {
	return e.mode === "float" ? e.x + e.w / 2 < h() / 2 ? "bottom-left" : "bottom-right" : e.side === "left" ? "bottom-left" : e.side === "top" ? "top-right" : "bottom-right";
}
var E = "--weft-devtools-inset", ne = `var(${E})`, re = class {
	prev = null;
	apply(e, t) {
		let n = document.documentElement.style, r = e ? `padding-${e}` : "";
		if (this.prev && this.prev.prop !== r && this.restore(), !e) return;
		let i = (e) => [n.getPropertyValue(e), n.getPropertyPriority(e)], a = n.getPropertyValue(r) === ne;
		this.prev ? a || (this.prev.was[1] = i(r)) : this.prev = {
			prop: r,
			was: [i(E), i(r)]
		}, n.getPropertyValue(E) !== t && n.setProperty(E, t), a || n.setProperty(r, ne);
	}
	restore() {
		let e = this.prev;
		if (!e) return;
		this.prev = null;
		let t = document.documentElement.style;
		[E, e.prop].forEach((n, r) => {
			let [i, a] = e.was[r];
			i ? t.setProperty(n, i, a) : t.removeProperty(n);
		});
	}
};
//#endregion
//#region src/panel/theme.ts
function ie(e) {
	let t = String(e ?? "").trim().toLowerCase();
	return t === "light" || t === "dark" ? t : "auto";
}
var ae = (e) => {
	let t = ie(e);
	return t === "auto" ? "" : t;
};
function oe() {
	try {
		let e = document.documentElement;
		return ae(e.getAttribute("data-theme")) || (e.classList.contains("dark") ? "dark" : e.classList.contains("light") ? "light" : ae(getComputedStyle(e).getPropertyValue("color-scheme")));
	} catch {
		return "";
	}
}
var se = "(prefers-color-scheme: dark)";
function ce(e) {
	try {
		return typeof window.matchMedia == "function" ? window.matchMedia(e) : null;
	} catch {
		return null;
	}
}
function le() {
	let e = ce(se);
	return e ? e.matches ? "dark" : ce("(prefers-color-scheme: light)")?.matches ? "light" : "" : "";
}
function ue(e, t, n = oe) {
	return e === "auto" ? ae(t) || n() || le() || "dark" : e;
}
var de = (e) => e === "auto" ? "light" : e === "light" ? "dark" : "auto", fe = class {
	obs = null;
	mq = null;
	cb = () => {};
	cached = null;
	fire = () => {
		this.cached = null, this.cb();
	};
	host = () => this.obs ? this.cached ??= oe() : oe();
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
		this.mq = ce(se);
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
function pe(e) {
	let t = new Set(String(e ?? "").toLowerCase().split(",").map((e) => e.trim()).filter(Boolean));
	if (t.has("off")) return "off";
	let n = t.delete("headers"), r = t.delete("markers");
	return t.size ? "" : n && r ? "headers,markers" : n ? "headers" : r ? "markers" : "";
}
var me = [
	"bottom-right",
	"bottom-left",
	"right-dock",
	"left-dock",
	"top-dock",
	"bottom-dock"
], he = (() => {
	try {
		let e = document.currentScript;
		return e && e.tagName === "SCRIPT" ? e : null;
	} catch {
		return null;
	}
})(), ge = (() => {
	try {
		return String(import.meta.url ?? "");
	} catch {
		return "";
	}
})();
function _e() {
	if (he?.isConnected) return he;
	try {
		let e = document.querySelector("script[data-weft]");
		if (e) return e;
		if (!ge) return null;
		for (let e of Array.from(document.querySelectorAll("script[src]"))) try {
			if (new URL(e.getAttribute("src") ?? "", document.baseURI).href === ge) return e;
		} catch {}
		return null;
	} catch {
		return null;
	}
}
function ve(e, t) {
	try {
		let n = new URL(e, t);
		return n.protocol !== "http:" && n.protocol !== "https:" ? "" : (n.pathname.endsWith("/") || (n.pathname += "/"), n.toString());
	} catch {
		return "";
	}
}
function ye(e) {
	let t = e?.getAttribute("src");
	if (!t) return "";
	try {
		return ve("./", new URL(t, document.baseURI).toString());
	} catch {
		return "";
	}
}
function be(e) {
	return (t) => {
		if (!e) return null;
		let n = {
			endpoint: e.endpoint,
			scope: xe(e.scope),
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
function xe(e) {
	if (typeof e == "string") return e;
	if (e && typeof e == "object" && typeof e.publicId == "string") try {
		return r(e);
	} catch {
		return;
	}
}
function Se() {
	return (e) => {
		try {
			return document.querySelector(`meta[name="weft:${e}"]`)?.getAttribute("content") ?? null;
		} catch {
			return null;
		}
	};
}
var Ce = (e) => (t) => e?.getAttribute(`data-${t}`) ?? null;
function D(e) {
	let t = _e(), n = [
		be(e?.options),
		Ce(e),
		Se(),
		Ce(t)
	], r = (e) => {
		for (let t of n) {
			let n = t(e);
			if (n !== null && (e !== "endpoint" || n.trim() !== "")) return n;
		}
		return null;
	}, a = ye(t), o = r("endpoint"), s = o === null ? a || ve("./", document.baseURI) : ve(o, document.baseURI), c = r("position"), l = r("open"), u = null;
	for (let e of n) {
		let t = e("scope");
		if (t !== null) {
			u = i(t);
			break;
		}
		let n = e("public-id");
		if (n !== null) {
			u = { publicId: n };
			break;
		}
	}
	u ??= Ee();
	let d = r("detect"), f = r("mode") ?? "", p = (r("z-index") ?? "").trim();
	return {
		endpoint: s,
		endpointExplicit: o !== null,
		configURL: o === null && a ? a + "panel-config.json" : "",
		publicId: u.publicId,
		scope: u,
		scopeExplicit: !!(u.publicId || u.session || u.flow || u.run),
		urlScope: ke(),
		token: r("token") ?? "",
		detect: pe(d),
		position: me.includes(c) ? c : "bottom-right",
		open: l === "true" || l === "",
		mode: [
			"float",
			"dock",
			"pill",
			"hidden"
		].includes(f) ? f : "",
		push: r("push") === "true",
		zIndex: /^-?\d+$/.test(p) ? p : "",
		theme: ie(r("theme")),
		auto: r("auto") !== "false",
		global: !["off", "false"].includes((r("global") ?? "").trim().toLowerCase())
	};
}
async function we(e, t) {
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
		let i = ve(r.endpoint, e);
		return !i || new URL(i).origin !== new URL(e).origin ? "" : i;
	} catch {
		return "";
	}
}
function Te() {
	try {
		let e = window.__WEFT__?.publicId;
		return e == null ? "" : String(e);
	} catch {
		return "";
	}
}
function Ee() {
	try {
		let e = window.__WEFT__?.scope;
		if (typeof e == "string") return i(e);
		let t = xe(e);
		if (t !== void 0) return i(t);
	} catch {}
	return { publicId: Te() };
}
var De = { href: () => location.href };
function Oe(e) {
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
function ke(e = De.href()) {
	try {
		let t = new URL(e);
		for (let e of [t.search, t.hash]) {
			let t = Oe(e);
			if (t === null) continue;
			let n = i(t);
			if (n.publicId && !/[;=]/.test(n.publicId)) return n;
		}
	} catch {}
	return null;
}
function Ae(e, t = De.href()) {
	return e.detect === "off" ? !1 : e.detect.includes("headers") ? !0 : c(t) && c(e.endpoint) && !e.token.startsWith("weft_pt.");
}
function je(e, t = De.href()) {
	return e.detect === "off" ? !1 : e.detect.includes("markers") ? !0 : c(t) || O(e.token) !== "read";
}
function O(e) {
	if (!e.startsWith("weft_pt.")) return "";
	try {
		let t = e.slice(8).split(".")[0].replace(/-/g, "+").replace(/_/g, "/");
		return JSON.parse(atob(t + "=".repeat((4 - t.length % 4) % 4))).scope === "playground" ? "playground" : "read";
	} catch {
		return "read";
	}
}
function Me() {
	try {
		if (new URLSearchParams(location.search).get("weft") === "debug" || C()) return !0;
	} catch {}
	return !1;
}
//#endregion
//#region src/lib/api.ts
function Ne(e) {
	let t = e?.batches;
	return Array.isArray(t) ? { batches: t.map((e) => {
		let t = Pe(e.messages), n = !Array.isArray(e.messages) || t.length !== e.messages.length;
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
function Pe(e) {
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
function Fe(e) {
	return typeof e.badge == "string";
}
//#endregion
//#region src/lib/honesty.ts
var k = {
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
}, Ie = {
	truncated: {
		log_cap: {
			reason: "the app-log reader's candidate cap was reached before attribution; later lines of this run may be missing",
			fix: "log less in the run's trace (a busy subagent or sibling run counts too); phase 2 filters by span before the cap"
		},
		result_cap: {
			reason: "a tool result was cut by its result cap: the model saw a prefix and the marker",
			fix: "raise the tool's weft.MaxResultBytes"
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
}, Le = Object.keys(k);
function Re(e) {
	return e !== void 0 && Object.prototype.hasOwnProperty.call(k, e);
}
function ze(e) {
	return e < 1024 ? `${e} B` : e < 1048576 ? `${(e / 1024).toFixed(1)} KiB` : `${(e / 1048576).toFixed(1)} MiB`;
}
function Be(e) {
	if (typeof e != "object" || !e) return [];
	let t = e, n = [], r = t["weft.content"];
	(r === "stripped" || r === "none") && n.push({ hole: "stripped" }), r === "redacted" && n.push({ hole: "redacted" });
	let i = Number(t["weft.content.truncated_bytes"]);
	return Number.isFinite(i) && i > 0 && n.push({
		hole: "truncated",
		bytes: i
	}), n;
}
function Ve(e) {
	return `${Ie.truncated?.result_cap.reason ?? ""} (${ze(e)} cut)`;
}
function A(...e) {
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
		let t = Le.indexOf(e);
		return t < 0 ? Le.length : t;
	};
	return [...t.values()].sort((e, t) => n(e.hole) - n(t.hole));
}
function j(e) {
	let t = Re(e.hole) ? k[e.hole] : void 0, n = Re(e.hole) ? Ie[e.hole] : void 0, r = e.cause && n && Object.hasOwn(n, e.cause) ? n[e.cause] : void 0;
	return {
		label: e.hole === "truncated" && e.bytes && !e.cause ? `shortened by the recorder: ${ze(e.bytes)} cut` : t?.label ?? e.hole,
		reason: e.reason || r?.reason || t?.reason || e.hole,
		fix: e.fix === "" ? void 0 : e.fix || r?.fix || t?.fix,
		tone: t?.tone ?? "loss"
	};
}
function He(e) {
	let t = Array.isArray(e.holes) ? [...e.holes] : [];
	return e.requests_badge === "not_recorded" && t.push({ hole: "not_recorded" }), e.status === "interrupted" && t.push({ hole: "interrupted" }), A(t);
}
function Ue(e) {
	let t = [];
	e.status === "interrupted" && t.push({ hole: "interrupted" });
	let n = Array.isArray(e.gaps) ? e.gaps : [];
	return n.length && e.status && e.status !== "running" && t.push({
		hole: "gap",
		reason: `${n.length} ${n.length === 1 ? "event" : "events"} missing (${n.length === 1 ? "position" : "positions"} ${n.slice(0, 8).join(", ")}${n.length > 8 ? ", …" : ""}): a destination dropped a batch`
	}), e.stop_reason === "max_tokens" && t.push({ hole: "max_tokens" }), t;
}
function We(e) {
	return e === "succeeded" || e === "failed";
}
var Ge = "usage at finish";
//#endregion
//#region src/lib/requests.ts
function Ke(e) {
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
function qe(e) {
	let t = e.body.params, n = (e) => e == null ? "adapter default" : JSON.stringify(e);
	return [
		["temperature", n(t.temperature)],
		["top_p", n(t.top_p)],
		["max_tokens", n(t.max_tokens)],
		["stop", t.stop === void 0 && e.content === "stripped" ? "not recorded (stripped)" : n(t.stop)],
		["seed", n(t.seed)]
	];
}
function Je(e) {
	return qe(e).map(([e, t]) => `${e} ${t}`).join(" · ");
}
var Ye = "request not recorded by weft v0.9.0 or earlier", Xe = "this step ran, but no request record names it";
function Ze(e) {
	return {
		reason: `the panel reads a run's first ${e} requests; this step's are past them`,
		fix: "open the run in Studio (⤢)"
	};
}
var Qe = "the transcript does not hold the messages this request counts", $e = "no messages record names this request (its compaction view could not be recorded)", et = "diff not drawn: the prompt was cut", tt = "not stored yet — the run is still running";
//#endregion
//#region src/lib/events.ts
function N(e) {
	return typeof e == "string" ? e : "";
}
function nt(e, t) {
	return typeof e == "number" && Number.isInteger(e) && e >= 0 ? e : t;
}
function rt(e) {
	return typeof e == "number" && Number.isFinite(e) && e > 0;
}
function it(e) {
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
			let f = Be(d);
			f.length && (t[i] = f, e.holes = A(e.holes, f));
			let p, m;
			if (typeof l == "object" && l) {
				switch (l.type) {
					case "run_start":
						e.runId = N(l.id), e.agent = typeof l.agent == "string" ? l.agent : void 0, e.model = l.model, e.startPos = i;
						break;
					case "step_start":
						a = !0, p = o(nt(l.index, c()));
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
						p = o(nt(l.index, c())), p.finish = {
							reason: N(l.reason),
							raw: l.raw,
							usage: it(l.usage)
						}, rt(l.latency_ms) && (p.finish.latencyMs = l.latency_ms), rt(l.ttft_ms) && (p.finish.ttftMs = l.ttft_ms);
						break;
					case "steered": {
						let e = o(nt(l.step, c()));
						p = e;
						let t = (Array.isArray(l.messages) ? l.messages : []).map((e) => st(e)).filter(Boolean).join("\n");
						e.steer = {
							text: (e.steer?.text ? e.steer.text + "\n" : "") + t,
							pos: i
						};
						break;
					}
					case "run_finish": e.finished = !0, e.usage = it(l.usage), e.pending = Array.isArray(l.pending) ? l.pending : [], e.finishPos = i;
				}
				f.length && (p && (p.holes = A(p.holes, f)), m && (m.holes = A(m.holes, f)));
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
function at(e, t) {
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
function ot(e) {
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
function st(e, t = "") {
	let n = e?.content;
	return Array.isArray(n) ? n.filter((e) => e?.type === "text" && typeof e.text == "string").map((e) => e.text).join(t) : "";
}
function ct(e) {
	let t = Array.isArray(e) ? e : [], n = lt(t);
	return t.map((e, t) => {
		let r = ot(e), i = e?.step, a = e?.input, o = e?.badge;
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
function lt(e) {
	let t = -1;
	return e.map((e, n) => {
		let r = ot(e), i = e?.input, a = typeof i == "boolean" ? i : n === 0 && (r.length !== 1 || r[0].role !== "assistant");
		return !a && r.some((e) => e.role === "assistant") && t++, {
			step: Math.max(t, 0),
			input: a
		};
	});
}
function ut(e) {
	let t = [], n = [];
	for (let r of ct(e)) (r.input ? t : n).push(...r.messages);
	return {
		input: t,
		produced: n
	};
}
function dt(e) {
	let { input: t } = ut(e);
	for (let e = t.length - 1; e >= 0; e--) {
		if (t[e].role !== "user") continue;
		let n = st(t[e], "\n");
		if (n) return n;
	}
	return null;
}
function ft(e, t, n) {
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
	for (let e of ct(t)) {
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
			let i = st(t);
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
function pt(e, t, n) {
	n?.status === "running" && (n = void 0);
	let r = [...e.holes ?? []];
	return e.derived && r.push({
		hole: "derived",
		reason: "this step's words come from a transcript batch whose step was inferred, not stored"
	}), e.finish?.reason === "max_tokens" && r.push({ hole: "max_tokens" }), A(Array.isArray(n?.holes) ? n.holes : [], r, (t ?? []).filter((e) => e.hole === "not_recorded" || e.hole === "stripped"));
}
function mt(e, t) {
	return A(Array.isArray(e?.holes) ? e.holes : [], t.holes);
}
function ht(e, t) {
	return e.state === "done" ? "done" : t === "running" ? "running" : "never";
}
var gt = /…\[truncated (\d+) bytes\]/u, _t = /^tool call (.+) was not executed: the response hit the output token limit$/, vt = "this call was not executed: the response hit the output token limit, and the loop retried the step with a full budget";
function yt(e) {
	let t = gt.exec(e);
	if (t) return {
		kind: "bytes",
		bytes: Number(t[1])
	};
	let n = _t.exec(e);
	return n ? {
		kind: "call",
		tool: n[1]
	} : null;
}
//#endregion
//#region src/panel/render.ts
function F(e, t, n, r) {
	let i = document.createElement(e);
	if (t && (i.className = t), typeof n == "string") i.textContent = n;
	else if (Array.isArray(n)) for (let e of n) i.appendChild(e);
	if (r) for (let [e, t] of Object.entries(r)) i.setAttribute(e, t);
	return i;
}
function I(e, t, n, r = !1) {
	let i = e._on ??= {}, a = r ? `${t}!` : t;
	return a in i || e.addEventListener(t, (t) => e._on?.[a]?.(t, e), r), i[a] = n, e;
}
var bt = (e) => e.nodeType === 1 ? e.getAttribute("data-key") : null, xt = (e, t) => e.nodeName === t.nodeName && bt(e) === bt(t), St = (e) => e.nodeType === 1 ? e.className : "";
function Ct(e, t) {
	let n = Array.from(e.childNodes), r = /* @__PURE__ */ new Map(), i = [];
	for (let e of n) {
		let t = bt(e);
		t === null ? i.push(e) : r.set(t, [...r.get(t) ?? [], e]);
	}
	let a = t.map((e) => {
		let t = bt(e);
		if (t !== null) {
			let n = r.get(t)?.shift();
			return [e, n && xt(n, e) ? n : void 0];
		}
		let n = i.findIndex((t) => xt(t, e) && St(t) === St(e));
		return n < 0 && (n = i.findIndex((t) => xt(t, e))), [e, n < 0 ? void 0 : i.splice(n, 1)[0]];
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
		r === c ? c = c.nextSibling : e.insertBefore(r, c), n && wt(n, t);
	}
}
function wt(e, t) {
	if (e.nodeType !== 1) {
		e.nodeValue !== t.nodeValue && (e.nodeValue = t.nodeValue);
		return;
	}
	let n = e, r = t;
	for (let e of Array.from(n.attributes)) r.hasAttribute(e.name) || n.removeAttribute(e.name);
	for (let e of Array.from(r.attributes)) n.getAttribute(e.name) !== e.value && n.setAttribute(e.name, e.value);
	let i = r._on, a = n._on;
	if (a) for (let e in a) i?.[e] || (a[e] = void 0);
	if (i) for (let e in i) I(n, e.replace("!", ""), i[e], e.endsWith("!"));
	Ct(n, Array.from(r.childNodes)), /^(INPUT|TEXTAREA|SELECT)$/.test(n.tagName) && n.value !== r.value && (n.value = r.value), n.tagName === "INPUT" && n.checked !== r.checked && (n.checked = r.checked);
}
function Tt(e, t) {
	return JSON.stringify(e, null, t);
}
function Et(e) {
	try {
		return Tt(e, 2) ?? String(e);
	} catch {
		return String(e);
	}
}
function Dt(e) {
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
function Ot(e) {
	let { parsed: t, from: n, to: r } = Dt(e);
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
function kt(e, t) {
	return t ? `${e} — fix: ${t}` : e;
}
function L(e, t = {}) {
	let n = j({
		hole: e,
		...t
	}), r = F("span", `weft-badge ${n.tone === "loss" ? "weft-warn-badge" : "weft-info"}`, t.label ?? n.label, {
		title: kt(n.reason, n.fix),
		"data-hole": e
	});
	return r.appendChild(F("span", "weft-sr", ` — ${kt(n.reason, n.fix)}`)), r;
}
function At(e) {
	if (!e.length) return null;
	let t = F("span", "weft-holes");
	for (let n of e) t.appendChild(L(n.hole, n));
	return t;
}
function jt(e) {
	let t = j(e), n = L(e.hole, e);
	return n.lastChild?.remove(), F("div", `weft-note${t.tone === "loss" ? " weft-warn" : ""}`, [n, document.createTextNode(` — ${t.reason}${t.fix ? ` · fix: ${t.fix}` : ""}`)]);
}
function Mt(e, t) {
	let n = e === "not_recorded" && !t ? Ye : j({ hole: e }).label;
	return n.startsWith("request") ? n : `request: ${n}`;
}
function R(e, t = {}) {
	let n = j({
		hole: e,
		...t
	}), r = L(e, {
		...t,
		label: t.label ?? Mt(e, t.cause)
	});
	return r.lastChild?.remove(), [r, F("div", "weft-reason", [n.reason, n.fix && `fix: ${n.fix}`].filter(Boolean).join(" — "))];
}
function Nt(e) {
	return L("truncated", {
		label: `request: ${k.truncated.label} — first ${e.toLocaleString("en-US").replace(",", " ")} requests`,
		...Ze(e)
	});
}
function Pt(e) {
	return e.kind === "bytes" ? L("truncated", {
		cause: "result_cap",
		reason: Ve(e.bytes)
	}) : L("max_tokens", {
		reason: vt,
		fix: ""
	});
}
function Ft(e, t) {
	let n = j({
		hole: "not_recorded",
		cause: "no_public_id",
		reason: e,
		fix: t
	});
	return `${n.label}: ${n.reason} · fix: ${n.fix}`;
}
var It = {
	none: "captured none (weft.Content(false), or no destination takes content)",
	stripped: "otel.NoContent()"
};
function Lt(e, t, n = 0) {
	let r = e?.events.find((e) => typeof e.attrs?.["weft.content"] == "string")?.attrs?.["weft.content"], i = t?.content?.latest, a = !r && i && (!e || i.run_id === e.id) ? i : null, o = r ?? a?.mark ?? "", s = e?.capped && n ? ` · first ${n} events` : "";
	if (Object.hasOwn(It, o)) {
		let e = j({
			hole: "stripped",
			reason: a?.note,
			fix: a?.fix
		});
		return {
			text: `content off · ${It[o]}${s}`,
			title: kt(e.reason, e.fix),
			hole: "stripped"
		};
	}
	let c = 0, l = 0;
	for (let t of Object.values(e?.folded.eventHoles ?? {})) for (let e of t) e.hole === "truncated" && (c++, l += e.bytes ?? 0);
	if (c) {
		let e = j({ hole: "truncated" });
		return {
			text: `content on · ${c} ${c === 1 ? "event" : "events"} shortened (${ze(l)} cut)${s}`,
			title: kt(e.reason, e.fix),
			hole: "truncated"
		};
	}
	return o === "full" ? {
		text: `content on${s}`,
		title: a?.note || "the record marks this run's content stored in full"
	} : null;
}
var Rt = {
	provider: "weft/runtime",
	name: "scripted"
};
function zt(e, t) {
	let n = [], r = (e, t, r) => n.push(F("span", "weft-chip weft-tag", e, {
		title: t,
		"data-weft-chip": r
	}));
	e.model.provider === Rt.provider && e.model.name === Rt.name && r(e.usage.input_tokens + e.usage.output_tokens === 0 ? "scripted (0 tokens)" : "scripted", "model weft/runtime/scripted: the scripted engine replayed the source run's recorded turns", "scripted");
	let i = e.meta["weft.session.forked_from"];
	if (i && r(`fork of ${i}`, `weft.session.forked_from = ${i}: a thread fork's run (<session>#<entry>)`, "fork"), e.forked_from) {
		let [n, i] = e.forked_from.split("#");
		r(`experiment of ${t(n)}`, `weft.forked_from = ${e.forked_from}: a playground experiment of run ${n}${i ? ` from step ${i}` : ""}${e.experiment_id ? ` (weft.experiment.id ${e.experiment_id})` : ""}`, "experiment");
	} else e.experiment_id && r("experiment", `weft.experiment.id = ${e.experiment_id}`, "experiment");
	return n;
}
//#endregion
//#region src/lib/live.ts
function Bt(e) {
	let [[t, n]] = Object.entries(e);
	return `${encodeURIComponent(t)}=${encodeURIComponent(n)}`;
}
var Vt = 6e4, Ht = class extends Error {
	status;
	constructor(e) {
		super(`live grant refused: ${e}`), this.status = e;
	}
};
function Ut(e) {
	return (e ?? ["event", "run"]).join(",");
}
async function Wt(e, t, n, r, i) {
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
			kinds: Ut(r)
		}),
		signal: i
	});
	if (!o.ok) throw new Ht(o.status);
	let s = await o.json();
	if (typeof s?.sig != "string" || !s.sig) throw new Ht(o.status);
	return {
		sig: s.sig,
		exp: Gt(s.exp, o.headers.get("Date"))
	};
}
function Gt(e, t, n = Date.now()) {
	let r = typeof e == "string" ? Date.parse(e) : NaN, i = t ? Date.parse(t) : NaN;
	return !Number.isFinite(r) || !Number.isFinite(i) ? n + Vt : n + Math.min(Vt, Math.max(0, r - i - 1e3));
}
function Kt(e, t, n, r) {
	let i = new URL(e);
	return i.search = Bt(t), i.searchParams.set("kinds", Ut(n)), i.searchParams.set("sig", r.sig), i.toString();
}
function qt(e, t = Date.now()) {
	return t >= e.exp;
}
//#endregion
//#region src/panel/client.ts
function Jt(e, t) {
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
	let i = await fetch(Jt(e, t), {
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
function Yt(e, t) {
	return B(e, "meta", t);
}
function Xt(e, t, n) {
	let r = new URLSearchParams(t).toString();
	return B(e, `runs${r ? `?${r}` : ""}`, n);
}
function Zt(e, t, n) {
	return B(e, `runs/${encodeURIComponent(t)}`, n);
}
function Qt(e, t, n, r) {
	return B(e, `runs/${encodeURIComponent(t)}/events?after=${n}&limit=500`, r);
}
function $t(e, t, n) {
	return B(e, `runs/${encodeURIComponent(t)}/transcript`, n).then(Ne);
}
var en = 1e3;
async function tn(e, t, n) {
	let r = [], i = 0;
	for (let a = 0; a < 10; a++) {
		let a;
		try {
			a = await B(e, `runs/${encodeURIComponent(t)}/requests?limit=${en}${i ? `&from=${i}` : ""}`, n);
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
async function nn(e, t) {
	let n = await B(e, `runs/${encodeURIComponent(t)}/tools`).catch(() => null);
	return n?.badge === "stripped" ? {
		badge: n.badge,
		reason: n.reason,
		fix: n.fix
	} : void 0;
}
function rn(e, t, n) {
	return B(e, `runs/${encodeURIComponent(t)}/spans`, n);
}
function an(e, t, n) {
	let r = new URLSearchParams(t).toString();
	return B(e, `sessions${r ? `?${r}` : ""}`, n);
}
function on(e, t, n) {
	return B(e, `sessions/${encodeURIComponent(t)}/public_id`, n);
}
function sn(e, t) {
	return B(e, "runtimes", t);
}
function cn(e, t) {
	return mn(e, "playground/runs", t);
}
function ln(e, t) {
	return B(e, `playground/commands/${encodeURIComponent(t)}`);
}
function un(e, t, n) {
	return mn(e, `runs/${encodeURIComponent(t)}/approvals`, n);
}
function dn(e, t, n) {
	return pn(e, `runtimes/${encodeURIComponent(t)}/breakpoints`, { tools: n });
}
function fn(e, t, n) {
	return mn(e, `runs/${encodeURIComponent(t)}/steer`, { message: n });
}
async function pn(e, t, n) {
	let r = {
		Accept: "application/json",
		"Content-Type": "application/json"
	};
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(Jt(e, t), {
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
async function mn(e, t, n) {
	let r = {
		Accept: "application/json",
		"Content-Type": "application/json"
	};
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(Jt(e, t), {
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
var hn = 1e4;
function gn(e, t) {
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
		Wt(Jt(e, "live-grant"), e.token, t.selector, t.kinds).then((e) => {
			!n && r === o && f(e);
		}, (e) => {
			if (!(n || r !== o)) {
				if (t.onRefused && e instanceof Ht && e.status === 403) {
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
			f = new EventSource(Kt(Jt(e, "live"), t.selector, t.kinds, o));
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
				if (r = !0, f.readyState === EventSource.CLOSED || qt(o)) {
					if (f.close(), s >= 1) {
						u("closed");
						return;
					}
					s++, d();
				}
				i ||= setTimeout(() => {
					i = null, !(n || a?.readyState === EventSource.OPEN) && u("closed");
				}, hn);
			}
		};
	};
	return d(), { close: l };
}
//#endregion
//#region src/lib/diff.ts
function _n(e, t) {
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
function vn(e) {
	let t = e.filter((e) => e.kind === "add").length, n = e.filter((e) => e.kind === "del").length;
	return !t && !n ? "identical" : `+${t} −${n}`;
}
var yn = 2e3;
function bn(e, t, n = yn) {
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
	})) : _n(s.join("\n"), c.join("\n"));
	return { rows: [
		...l(r.slice(0, a)),
		...u,
		...l(r.slice(r.length - o))
	] };
}
//#endregion
//#region src/lib/format.ts
function xn(e, t = Date.now()) {
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
function Sn(e, t) {
	let n = Date.parse(e), r = t ? Date.parse(t) : NaN;
	return Number.isNaN(n) || Number.isNaN(r) || r < n ? "—" : Cn(r - n);
}
function Cn(e) {
	if (!Number.isFinite(e)) return "—";
	if (e < 1e3) return `${Math.round(e)}ms`;
	if (e < 59950) return `${(e / 1e3).toFixed(1)}s`;
	let t = Math.round(e / 1e3), n = Math.floor(t / 60);
	return n < 60 ? `${n}m${String(t % 60).padStart(2, "0")}s` : `${Math.floor(n / 60)}h${String(n % 60).padStart(2, "0")}m`;
}
function V(e) {
	if (!Number.isFinite(e)) return "—";
	let t = Math.abs(e);
	return t >= 999950 ? `${(e / 1e6).toFixed(1)}M` : t >= 1e3 ? `${(e / 1e3).toFixed(1)}k` : String(e);
}
//#endregion
//#region src/lib/compaction.ts
var wn = (e, t) => `${e} ${t}${e === 1 ? "" : "s"}`;
function H(e) {
	return e.scope === "session";
}
function Tn(e) {
	return Array.isArray(e?.compactions) ? e.compactions : [];
}
function En(e) {
	if (H(e)) {
		let t = e.tokens_before && e.tokens_after ? ` · ${V(e.tokens_before)} → ${V(e.tokens_after)} tokens` : "";
		return `${wn(e.replaced, "message")} compacted into ${e.entries}${t}`;
	}
	return e.replaced === 0 ? `${wn(e.entries, "message")} inserted by PrepareStep` : `${wn(e.replaced, "message")} rewritten into ${e.entries} by PrepareStep`;
}
var Dn = "session compaction · after this run";
function On(e) {
	return `thread compacted the session context this run belongs to: ${e.replaced} of its messages were replaced by ${e.entries}; the next run starts on the compacted context (its input record). The marker carries counts and a hash, never messages. (For entries appended by hand that no run produced, thread files the marker under the run that follows, which starts on the compacted context.)`;
}
function kn(e, t, n) {
	if (!t) return { loading: !0 };
	let r = e.index ?? -1, i = e.from_seq ?? -1, a = e.to_seq ?? -1;
	if (r < 0 || i < 0 || a < i) return { gap: "the view names no usable range: its replaced messages cannot be placed" };
	let o = new Set(n.filter((e) => !H(e) && e.index != null).map((e) => e.index)), s = (Array.isArray(t.batches) ? t.batches : []).filter((e) => e !== null && typeof e.index == "number" && e.index < r).sort((e, t) => e.index - t.index), c = new Set(s.map((e) => e.index)), l = [];
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
function An(e, t = 160) {
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
function jn(e, t) {
	let n = t == null ? "" : ` — the step's request carried ${wn(t, "message")} in all`;
	return `in their place, ${wn(e.entries, "message")}${n}; the view's body stays in its record (export the run: compactions[].messages)`;
}
//#endregion
//#region src/lib/attempts.ts
function Mn(e, t, n = !1) {
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
function Nn(e) {
	if (!e) return null;
	if (e.n === 0) return e.total > 0 ? `${e.total} ${e.total === 1 ? "attempt" : "attempts"} · none answered` : null;
	if (e.n <= 1) return null;
	let t = `attempt ${e.n} of ${Math.max(e.total, e.n)}`;
	return e.requested && e.answered && (t += e.answered === e.requested ? " · retry" : ` · fallback to ${e.answered}`), t;
}
function Pn(e) {
	if (e < 1e3) return `${Math.round(e)} ms`;
	if (e < 59950) return `${(e / 1e3).toFixed(1)} s`;
	let t = Math.round(e / 1e3);
	return `${Math.floor(t / 60)}m${String(t % 60).padStart(2, "0")}s`;
}
function Fn(e, t, n = "first token") {
	let r = [];
	return e && e > 0 && r.push(Pn(e)), t && t > 0 && r.push(`${n} ${Pn(t)}`), r.length ? r.join(" · ") : null;
}
function In(e, t) {
	return e === void 0 || e.latencyMs || t !== 0 ? null : {
		hole: "not_recorded",
		reason: "this step's step_finish has no timing and its request record no attempt rows: it was recorded by a weft before attempt reporting (A4)"
	};
}
//#endregion
//#region src/panel/markers.ts
var Ln = "data-weft-scope", Rn = "data-weft-untrusted", zn = (e) => !!e.closest(`[${Rn}]`), Bn = "weft-devtools";
function Vn(e) {
	try {
		if (!(e instanceof Element) || e.closest(Bn)) return null;
		let t = e.closest(`[${Ln}]`);
		for (; t && zn(t);) t = t.parentElement?.closest("[data-weft-scope]") ?? null;
		return t;
	} catch {
		return null;
	}
}
function Hn(e) {
	if (e.type === "attributes") return !(e.target instanceof Element && e.target.closest(Bn));
	for (let t of [...Array.from(e.addedNodes), ...Array.from(e.removedNodes)]) if (t instanceof Element && (t.hasAttribute("data-weft-scope") || t.querySelector("[data-weft-scope]"))) return !0;
	return !1;
}
function Un(e = document) {
	let t = [];
	for (let n of Array.from(e.querySelectorAll(`[${Ln}]`))) {
		if (n.closest(Bn) || zn(n)) continue;
		let e = i(n.getAttribute("data-weft-scope") ?? "");
		e.publicId && t.push({
			scope: e,
			element: n
		});
	}
	return t;
}
function Wn(e) {
	let t = e.root ?? document, n = null, r = null, i = 0, a = !1, o = () => {
		if (n = null, i = 0, t.hidden) {
			a = !0;
			return;
		}
		a = !1;
		try {
			e.onScopes(Un(t));
		} catch {}
	}, s = () => {
		a && !t.hidden && o();
	}, c = (t) => {
		let n = Vn(t.target);
		if (n) try {
			e.onFocus?.(n);
		} catch {}
	};
	try {
		r = new MutationObserver((t) => {
			if (!t.some(Hn)) return;
			let r = Date.now();
			i ||= r, n && clearTimeout(n), n = setTimeout(o, Math.max(0, Math.min(e.debounceMs ?? 100, i + 500 - r)));
		}), r.observe(t.documentElement, {
			subtree: !0,
			childList: !0,
			attributes: !0,
			attributeFilter: [Ln, Rn]
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
var Gn = {
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
}, Kn = "ui-monospace, SFMono-Regular, Menlo, Consolas, \"Liberation Mono\", monospace", qn = (e) => Object.entries(Gn[e]).map(([e, t]) => `--weft-${e}: ${t};`).join(" ");
[...Object.keys(Gn.dark).map((e) => `--weft-${e}`)];
var Jn = `
:host { all: initial; color: inherit; box-sizing: border-box; color-scheme: dark; ${qn("dark")}
  --weft-font: ${Kn}; --weft-radius: 8px; --weft-radius-sm: 5px; }
:host([data-theme-resolved="light"]) { color-scheme: light; ${qn("light")} }
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
.weft-resolve { width: auto; min-width: 150px; margin: 0 4px; }
`;
//#endregion
//#region src/panel/playground.ts
function Yn(e) {
	return e.content.filter((e) => e?.type === "text").map((e) => e.text).join("");
}
function Xn(e, t) {
	let n = "";
	if (t !== void 0) try {
		n = Tt(t) ?? "";
	} catch {
		n = "?";
	}
	return `${e}(${n})`;
}
function Zn(e) {
	return e ? dt(e.batches) ?? "" : "";
}
function Qn(e) {
	if (!e || !e.batches.length) return null;
	let { input: t, produced: n } = ut(e.batches), r = t.length;
	for (let e = t.length - 1; e >= 0 && t[e].role !== "user"; e--) r = e;
	let i = [...t.slice(r), ...n].filter((e) => e.role === "assistant"), a = [];
	for (let e of i) for (let t of e.content) t?.type === "tool_call" && a.push(Xn(t.name, t.args));
	return {
		text: i.map(Yn).filter(Boolean).join("\n"),
		calls: a
	};
}
function $n(e) {
	return {
		text: e.steps.map((e) => e.text).filter(Boolean).join("\n"),
		calls: e.steps.flatMap((e) => e.toolCalls).map((e) => Xn(e.name, e.args))
	};
}
function er(e, t) {
	let n = /-t(\d+)$/.exec(e);
	return `${n ? `t${n[1]}` : e.slice(-8)}·x${t + 1}`;
}
function tr(e) {
	let t = Object.keys(e.tools);
	return t.length && !t.some((t) => e.tools[t]) ? "at least one tool must stay on — the command cannot express an empty tool set (it would run with every tool)" : null;
}
function nr(e, t) {
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
function rr(e, t) {
	let n = e.filter((e) => e.agents.some((e) => e.name === t));
	if (!n.length) return e[0] ?? null;
	let r = (e) => Date.parse(e.last_seen) || 0;
	return n.reduce((e, t) => r(t) > r(e) ? t : e);
}
//#endregion
//#region src/panel/version.ts
function ir() {
	return "v0.11.0";
}
function ar(e) {
	if (typeof e != "string") return null;
	let t = /^v?(\d+(?:\.\d+)*)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]*)?$/.exec(e.trim());
	return t ? {
		nums: t[1].split(".").map(Number),
		pre: t.at(2) ?? ""
	} : null;
}
function or(e, t) {
	let n = ar(e), r = ar(t);
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
function sr(e) {
	return or(e, ir()) > 0;
}
//#endregion
//#region src/panel/state.ts
var cr = (e, t) => !!e && e.before === t.before && e.id === t.id, lr = 1e3, ur = 700, dr = 8, fr = 1e4, pr = 3e4, mr = 1e3;
function hr() {
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
function gr(e) {
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
function _r(e) {
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
function vr(e, t, n, r) {
	if (t && typeof t == "object" && typeof t.type == "string") try {
		e.push(t, n, r);
	} catch {}
}
function yr(e, t, n) {
	for (let r of n) r && typeof r.pos == "number" && !t.has(r.pos) && (t.add(r.pos), vr(e, r.event, r.pos, r.attrs));
}
function br(e, t) {
	let n = P(), r = /* @__PURE__ */ new Set();
	yr(n, r, t), e.feed = n, e.seen = r, e.events = t, e.pos = r.size ? Math.max(...r) : -1, e.stale = !0;
}
function xr(e, t, n = !1) {
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
	return vr(e.feed, t.event, r, t.attrs), e.stale = !0, "folded";
}
function U(e) {
	let t = e.result();
	return Array.isArray(t.pending) || (t.pending = []), t;
}
function Sr(e, t, n = !1) {
	if (!t) return e;
	try {
		return ft(e, t.batches, { replace: n });
	} catch {
		return e;
	}
}
var W = () => {}, Cr = class {
	state = hr();
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
		this.publicId = r.publicId, this.narrowing = Dr(r);
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
			let t = await Yt(this.ep);
			if (!t || typeof t != "object" || typeof t.studio_version != "string") throw Error("not a Studio");
			e = {
				...t,
				capabilities: Array.isArray(t.capabilities) ? t.capabilities : []
			};
		} catch {
			return this.state.gone = !0, !1;
		}
		return !this.disposed && (this.state.meta = e, this.state.tooNew = sr(e.studio_version), this.emit(), this.state.tooNew || await this.scope(), !0);
	}
	async rescope(e, t = {}) {
		let n = typeof e == "string" ? { publicId: e } : e, i = Dr(n), a = r({
			publicId: "",
			...i
		}) === r({
			publicId: "",
			...this.narrowing
		});
		if (n.publicId === this.publicId && a) return;
		let o = n.publicId === this.publicId, s = i.session === this.narrowing.session;
		if (i.run !== this.narrowing.run && (this.pinApplied = "", this.pinChecks = 0, this.cancel(this.pinTimer), this.pinTimer = null), t.force && (this.userSelected = !1), this.publicId = n.publicId, this.narrowing = i, this.state.meta && !this.state.tooNew) {
			if (!o) {
				await this.scope();
				return;
			}
			if (s && (!i.run || this.rowOf(i.run))) {
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
		this.scopeSub = gn(this.ep, {
			selector: { public_id: this.publicId },
			kinds: ["run"],
			onOpen: (e) => {
				this.retries.scope = 0, this.state.live || (this.state.live = !0, this.emit()), e && this.refresh().catch(W);
			},
			onRun: (e) => this.onRunFrame(e),
			onOverflow: (e) => {
				this.scopeSub = void 0, this.state.live = !1, this.emit(), e !== "expired" && (e === "overflow" ? this.overflowed("scope", t) : this.retry("scope", t));
			}
		}), this.state.live = !0, this.emit();
	}
	retry(e, t) {
		let n = this.retries[e];
		n >= 5 || (this.retries[e] = n + 1, this.after(Math.min(5e3 * 2 ** n, 6e4), () => void t().catch(W)));
	}
	overflowed(e, t) {
		let n = this.overflows[e], r = Date.now();
		n.n = n.at && r - n.at < 6e4 ? n.n + 1 : 0, n.at = r, this.after(Math.min(lr * 2 ** Math.min(n.n, 6), 6e4), () => void t().catch(W));
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
		this.cancel(this.devTimer), this.devTimer = null, !(!this.watching || this.publicId || this.disposed || !this.state.meta || this.state.tooNew) && (this.devTimer = this.after(this.devSub ? pr : fr, () => {
			if (this.devTimer = null, typeof document < "u" && document.visibilityState === "hidden") {
				this.armDev();
				return;
			}
			this.refresh().catch(W).then(() => this.armDev());
		}));
	}
	armStale() {
		if (this.cancel(this.staleTimer), this.staleTimer = null, this.disposed || ![...this.state.turns, ...[...this.state.experiments.values()].flat()].some((e) => e.status === "running")) return;
		let e = this.state.meta?.interrupted_after_ms, t = (typeof e == "number" && e > 0 ? Math.min(e, 6e5) : 3e4) + 1e3;
		this.staleTimer = this.after(t, () => {
			this.staleTimer = null, this.refresh().catch(W);
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
		if (O(this.ep.token) !== "") {
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
		this.devSub = gn(this.ep, {
			selector: { agent: n },
			kinds: ["run"],
			onOpen: (e) => {
				this.retries.dev = 0, e && this.refresh().catch(W);
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
				let t = await an(this.ep, { public_id: this.publicId }).catch(() => null);
				if (e !== this.loadSeq || this.disposed) return;
				if (t && Array.isArray(t.sessions)) {
					let e = this.narrowing.session;
					this.state.session = (e ? t.sessions.find((t) => t.id === e) : t.sessions[0]) ?? null;
				}
			}
			let n = await Xt(this.ep, this.publicId ? {
				public_id: this.publicId,
				limit: "50"
			} : { limit: "10" });
			if (e !== this.loadSeq || this.disposed) return;
			let { turns: r, experiments: i } = _r((Array.isArray(n.runs) ? n.runs : []).filter((e) => !!e && typeof e.id == "string")), a = this.narrowing.session, o = this.state;
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
			this.state.listKey = Er(this.publicId, this.narrowing.session);
		} catch {
			return;
		} finally {
			this.collectors.delete(t);
		}
		this.armStale(), this.publicId || (this.subscribeDev(e), this.armDev());
		let n = this.state.turn;
		if (n && !n.done && !n.loading) {
			let e = this.rowOf(n.id)?.status;
			e && e !== "running" && this.settleTurn(n).catch(W);
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
		return this.pinChecks++, t ? (this.state.pinMissing = "", this.pinApplied === e || this.userSelected ? "" : (this.pinApplied = e, e)) : (this.state.pinMissing = this.pinChecks >= 2 ? e : "", this.pinChecks === 1 && !this.pinTimer && (this.pinTimer = this.after(mr, () => {
			this.pinTimer = null, this.refresh().catch(W);
		})), "");
	}
	onRunFrame(e) {
		let t = e.run;
		if (this.publicId && t.public_id && t.public_id !== this.publicId || t.parent_run_id || !this.inSession(t)) return;
		for (let e of this.collectors) e.push(t);
		this.upsertRun(t), this.publicId || this.capDev(), this.armStale();
		let n = this.takePin();
		if (n) {
			this.select(n).catch(W);
			return;
		}
		if (!this.state.selected && this.state.turns.length) {
			let e = this.state.turns.find((e) => e.status === "running");
			this.select((e ?? this.state.turns[0]).id).catch(W);
			return;
		}
		let r = this.state.turn;
		r && r.id === t.id && (t.status === "running" ? r.done && !this.runSub && !r.loading && (r.done = !1, this.resumeTurn(r).catch(W)) : this.settleTurn(r).catch(W)), this.emit();
	}
	upsertRun(e, t = !1) {
		if (t && this.rowOf(e.id)) return;
		let n = this.state.turns.filter((t) => t.id !== e.id), r = /* @__PURE__ */ new Map();
		for (let [t, n] of this.state.experiments) {
			let i = n.filter((t) => t.id !== e.id);
			i.length && r.set(t, i);
		}
		let i = _r([e, ...n]);
		this.state.turns = i.turns;
		for (let [e, t] of i.experiments) r.set(e, [...r.get(e) ?? [], ...t]);
		this.state.experiments = r;
	}
	async walkEvents(e) {
		let t = [], n = [], r = 0;
		for (let i = 0; i < 20; i++) {
			let i = await Qt(this.ep, e, r);
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
		let n = gr(e);
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
			Zt(n, r).catch(() => null),
			this.walkEvents(r).catch(() => null),
			$t(n, r).catch(() => null),
			rn(n, r).then((e) => Array.isArray(e.spans) ? e.spans : null).catch(() => null),
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
				r && r !== n.status && (e.tried.delete(t), e.expanded.has(t) ? this.expandChild(t, !0).catch(W) : e.children.delete(t));
			}
			a && (br(e, a.events), e.gaps = a.gaps, e.capped = a.capped), o && (e.transcript = o), s && (e.spans = s), c && (e.requests = c), e.done || e.capped ? e.held.length = 0 : this.drain(e, r, () => this.state.turn === e && !e.done), e.recheck && !e.done && (e.recheck = !1, this.catchUp(e, r, () => this.state.turn === e && !e.done).catch(W)), this.dress(e), this.emit();
		}
	}
	async readRequests(e) {
		if (!this.state.meta?.capabilities.includes("requests")) return null;
		if (O(this.ep.token) === "read") return {
			badge: "hidden",
			reason: k.hidden.reason,
			fix: k.hidden.fix,
			steps: /* @__PURE__ */ new Map()
		};
		try {
			let t = await tn(this.ep, e), n = t.requests.some((e) => e.content === "stripped") ? await nn(this.ep, e) : void 0;
			return {
				badge: t.badge,
				reason: t.reason,
				fix: t.fix,
				truncated: t.truncated,
				steps: Ke(t.requests),
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
		for (let i = 0; i < r.length; i++) if (xr(e, r[i]) === "gap") {
			e.held.push(...r.slice(i)), this.catchUp(e, t, n).catch(W);
			return;
		}
	}
	liveInto(e, t, n, r) {
		if (e.loading || e.reading) {
			e.held.push(n);
			return;
		}
		let i = xr(e, n);
		i === "gap" ? (e.held.push(n), this.catchUp(e, t, r).catch(W)) : i === "folded" && this.emit(!0);
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
					let i = await Qt(this.ep, t, r);
					if (!n()) return;
					if (Array.isArray(i.events)) for (let t of i.events) t && typeof t.pos == "number" && !e.seen.has(t.pos) && (e.seen.add(t.pos), e.events.push(t), vr(e.feed, t.event, t.pos, t.attrs), t.pos > e.pos && (e.pos = t.pos), e.stale = !0);
					if (typeof i.next_after != "number" || i.next_after <= r) break;
					r = i.next_after;
				}
			} while (r());
		} catch {} finally {
			e.reading = !1;
		}
		if (n() && !i()) {
			for (let t of e.held.splice(0)) xr(e, t, !0);
			this.emit(!0);
		}
	}
	dress(e) {
		e.stale = !1;
		let t = this.rowOf(e.id)?.status ?? e.doc?.status;
		if (e.folded = Sr(U(e.feed), e.transcript, e.done || !!t && t !== "running"), e.doc && Array.isArray(e.doc.children)) try {
			at(e.folded, e.doc.children);
		} catch {}
	}
	follow(e) {
		let t = e.id;
		this.runSub?.close(), this.runSub = gn(this.ep, {
			selector: { run: t },
			kinds: [
				"event",
				"delta",
				"run"
			],
			onOpen: () => {
				this.retries.run = 0, this.state.turn === e && !e.done && !e.capped && this.catchUp(e, t, () => this.state.turn === e && !e.done).catch(W);
			},
			onRecord: (n) => {
				this.state.turn !== e || e.id !== n.run_id || e.done || e.capped || this.liveInto(e, t, n, () => this.state.turn === e && !e.done);
			},
			onRun: (n) => {
				n.run.id === t && this.state.turn === e && (this.rowOf(t) && this.upsertRun(n.run), n.run.status !== "running" && this.settleTurn(e).catch(W), this.emit());
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
		yr(a, /* @__PURE__ */ new Set(), i.events);
		let [o, s, c] = await Promise.all([
			$t(this.ep, e).catch(() => null),
			Zt(this.ep, e).catch(() => null),
			this.readRequests(e)
		]);
		if (r !== this.loadSeq || this.disposed || this.state.turn !== n) return;
		let l = n.doc?.children.find((t) => t.id === e)?.status ?? "running", u = Sr(U(a), o, l !== "running");
		if (s && Array.isArray(s.children)) try {
			at(u, s.children);
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
			t = await Zt(this.ep, e);
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
	async openExperiment(e, t = 0) {
		let n = this.rowOf(e);
		if (!n) return;
		let r = this.loadSeq, i;
		try {
			let e = await sn(this.ep);
			i = Array.isArray(e.runtimes) ? e.runtimes : [];
		} catch (t) {
			!this.disposed && r === this.loadSeq && this.setExperimentError(G(t), e);
			return;
		}
		if (this.disposed || r !== this.loadSeq) return;
		this.state.runtimes = i;
		let a = rr(i, n.agent), o = a?.agents.find((e) => e.name === n.agent);
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
			input: t === 0 && c && c.id === e ? Zn(c.transcript) : "",
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
				input: t.input || (n && n.id === e ? Zn(n.transcript) : "")
			};
		}
		await this.runExperiment();
	}
	async runExperiment() {
		let e = this.state.drawer;
		if (!e || this.posting) return;
		let t = tr(e);
		if (t) {
			this.setExperimentError(t, e.runId);
			return;
		}
		this.posting = !0;
		let n = this.loadSeq, r;
		try {
			r = await cn(this.ep, nr(e, this.publicId));
		} catch (t) {
			!this.disposed && n === this.loadSeq && this.setExperimentError(G(t), e.runId);
			return;
		} finally {
			this.posting = !1;
		}
		if (this.disposed || n !== this.loadSeq) return;
		let i = this.state.experiments.get(e.runId)?.length ?? 0, a = this.state.turn, o = a && a.id === e.runId ? Qn(a.transcript) ?? $n(a.folded) : {
			text: "",
			calls: []
		};
		this.expSub?.close(), this.expSub = void 0;
		let s = wr(e.runId, er(e.runId, i), o);
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
					r.error = `the parked run could not be read again — nothing was sent (${G(e)})`, this.emit();
					return;
				}
				if (this.left(r)) return;
				let a = P();
				yr(a, /* @__PURE__ */ new Set(), i.events);
				let o = U(a).pending;
				if (!o.some((t) => t.id === e)) {
					r.folded.pending = o, r.error = `call ${e} is not pending on ${r.runID} — nothing was sent`, this.emit();
					return;
				}
				let s;
				try {
					s = await un(this.ep, r.runID, {
						call_id: e,
						decision: t,
						content: n
					});
				} catch (e) {
					r.error = G(e), this.emit();
					let t = await this.walkEvents(r.runID).catch(() => null);
					if (t && !this.left(r)) {
						let e = P();
						yr(e, /* @__PURE__ */ new Set(), t.events), r.folded.pending = U(e).pending, this.emit();
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
		let n = Qn(await $t(this.ep, e).catch(() => null));
		if (!n) {
			let t = P();
			try {
				yr(t, /* @__PURE__ */ new Set(), (await this.walkEvents(e)).events);
			} catch {}
			n = $n(U(t));
		}
		this.left(t) || (this.compareWords.set(e, n), this.emit());
	}
	compareWords = /* @__PURE__ */ new Map();
	async setBreakpoints(e) {
		let t = this.state.drawer;
		if (!t) return;
		let n = this.loadSeq, r;
		try {
			r = await dn(this.ep, t.runtimeId, e);
		} catch (e) {
			!this.disposed && n === this.loadSeq && this.setExperimentError(G(e), t.runId);
			return;
		}
		this.disposed || n !== this.loadSeq || (this.state.breakpoints = Array.isArray(r?.tools) ? r.tools : e, this.emit());
	}
	async steer(e) {
		let t = this.state.result;
		if (t && t.runID && e) {
			try {
				await fn(this.ep, t.runID, e);
			} catch (e) {
				t.error = G(e);
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
			let n = wr(t ?? this.state.selected, "—", {
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
				r = await ln(this.ep, e.commandID);
			} catch (r) {
				if (this.left(e)) return;
				let i = r instanceof z && [
					401,
					403,
					404,
					410
				].includes(r.status);
				if (i || ++t >= dr) {
					e.state = "lost", e.error = i ? G(r) : "Studio stopped answering — the command's state is unknown", this.emit();
					return;
				}
				this.after(ur, () => void n().catch(W));
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
				this.after(ur, () => void n().catch(W));
			}
		};
		n().catch(W);
	}
	followExperiment(e, t) {
		let n = P();
		e.runID = t, e.row = null, e.events = [], e.seen = /* @__PURE__ */ new Set(), e.feed = n, e.folded = U(n), e.pos = -1, e.held = [], e.reading = !1, e.recheck = !1, e.loading = !1, e.stale = !1, e.ready = !1, e.words = null, e.deciding = null, e.decided = {}, this.retries.exp = 0, this.openExperimentStream(e);
	}
	openExperimentStream(e) {
		let t = e.runID, n = () => !this.left(e) && e.runID === t && !e.ready;
		this.expSub?.close(), this.expSub = gn(this.ep, {
			selector: { run: t },
			kinds: [
				"event",
				"delta",
				"run"
			],
			onOpen: () => {
				this.retries.exp = 0, !this.left(e) && e.runID === t && !e.ready && this.catchUp(e, t, n).catch(W);
			},
			onRecord: (r) => {
				this.state.result !== e || e.runID !== r.run_id || e.ready || this.liveInto(e, t, r, n);
			},
			onRun: (t) => {
				this.state.result === e && t.run.id === e.runID && (e.row = t.run, this.emit(), t.run.status !== "running" && this.settleExperiment(e, 0).catch(W));
			},
			onOverflow: (n) => {
				if (this.expSub = void 0, this.state.result !== e || e.runID !== t || e.ready) return;
				let r = async () => {
					this.state.result !== e || e.runID !== t || e.ready || (await this.loadExperiment(e), !(this.left(e) || e.runID !== t || Tr(e)) && (e.state === "queued" || e.state === "accepted") && this.openExperimentStream(e));
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
				$t(this.ep, t).catch(() => null),
				Zt(this.ep, t).catch(() => null)
			]);
		} finally {
			e.loading = !1;
		}
		if (this.disposed || this.state.result !== e || e.runID !== t) return null;
		n && n.events.length && br(e, n.events);
		let a = () => !this.left(e) && e.runID === t && !e.ready;
		return this.drain(e, t, a), e.recheck && (e.recheck = !1, this.catchUp(e, t, a).catch(W)), i && (e.row = i), e.folded = Sr(U(e.feed), r, !!e.row && e.row.status !== "running"), e.stale = !1, this.emit(), { transcript: r };
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
		if (!n || Tr(e)) return;
		let r = e.row;
		if ((!r || r.status === "running" || r.status === "succeeded" && !e.folded.finished) && t < 14) {
			this.after(1e3, () => void this.settleExperiment(e, t + 1).catch(W));
			return;
		}
		e.ready = !0, e.words = Qn(n.transcript) ?? $n(e.folded), this.expSub?.close(), this.expSub = void 0, this.emit();
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
			let r = await Xt(this.ep, {
				public_id: this.publicId,
				limit: "50",
				before: t.before,
				...t.id ? { before_id: t.id } : {}
			});
			if (n !== this.loadSeq || this.disposed || !cr(this.cursor, t)) return;
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
				t?.stale && (t.stale = !1, t.folded = U(t.feed)), this.notify(this.state);
			});
		}
	}
	dispose() {
		this.disposed = !0, this.raf && cancelAnimationFrame(this.raf), this.raf = 0, this.clearTimers(), this.scopeSub?.close(), this.runSub?.close(), this.expSub?.close(), this.devSub?.close(), this.scopeSub = this.runSub = this.expSub = this.devSub = void 0;
	}
};
function wr(e, t, n) {
	let r = P();
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
		folded: U(r),
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
function Tr(e) {
	return e.ready;
}
function G(e) {
	return e instanceof Error ? e.message : String(e);
}
function Er(e, t) {
	return r({
		publicId: e,
		session: t
	});
}
function Dr(e) {
	let t = {};
	return e.session && (t.session = e.session), e.flow && (t.flow = e.flow), e.run && (t.run = e.run), t;
}
//#endregion
//#region src/panel/tree.ts
var Or = 2048, kr = 200;
function Ar(e) {
	e.timer !== void 0 && e.applied !== e.q && (e.applied = e.q, e.open.clear()), clearTimeout(e.timer), clearTimeout(e.saidTimer), e.timer = e.saidTimer = void 0, e.said === "copied" && (e.said = "");
}
var jr = () => ({
	open: /* @__PURE__ */ new Map(),
	more: /* @__PURE__ */ new Map(),
	full: /* @__PURE__ */ new Set(),
	q: "",
	applied: "",
	said: "",
	box: "",
	bytes: /* @__PURE__ */ new Map()
}), K = (e) => typeof e == "object" && !!e, Mr = (e) => Array.isArray(e) ? e.length : Object.keys(e).length;
function Nr(e, t, n = Infinity) {
	if (Array.isArray(e)) for (let r = 0; r < Math.min(e.length, n); r++) t(String(r), e[r], r);
	else {
		let r = Object.keys(e);
		for (let i = 0; i < Math.min(r.length, n); i++) t(r[i], e[r[i]], i);
	}
}
var Pr = (e, t) => `${e}/${t.replace(/~/g, "~0").replace(/\//g, "~1")}`;
function Fr(e, t = 2) {
	try {
		return Tt(e, t) ?? String(e);
	} catch {
		return String(e);
	}
}
function Ir(e) {
	let t = /* @__PURE__ */ new Set();
	if (!K(e)) return t;
	let n = 0, r = [["", e]];
	for (let [e, i] of r) {
		let a = Math.min(Mr(i), 200);
		e && n + a > 150 || (t.add(e), n += a, Nr(i, (t, n) => K(n) && r.push([Pr(e, t), n]), 200));
	}
	return t;
}
function Lr(e, t) {
	let n = /* @__PURE__ */ new Set(), r = /* @__PURE__ */ new Set(), i = 0, a = t.toLowerCase(), o = [[
		"",
		null,
		e
	]];
	for (; o.length;) {
		let [e, t, s] = o.pop(), c = K(s) ? "" : String(s);
		if ((t !== null && t.toLowerCase().includes(a) || c.toLowerCase().includes(a)) && (i++, n.size < kr)) {
			n.add(e);
			for (let t = e.lastIndexOf("/"); t > 0; t = e.lastIndexOf("/", t - 1)) r.add(e.slice(0, t));
			r.add("");
		}
		K(s) && Nr(s, (t, n) => o.push([
			Pr(e, t),
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
function Rr(e, t = 0) {
	let n = 0;
	for (let r = t; r < e.length; r++) {
		let t = e.charCodeAt(r);
		t < 128 ? n += 1 : t < 2048 ? n += 2 : (t & 64512) == 55296 && (e.charCodeAt(r + 1) & 64512) == 56320 ? (n += 4, r++) : n += 3;
	}
	return n;
}
var zr = (e) => (e.charCodeAt(2047) & 64512) == 55296 ? 2047 : Or, Br = (e) => Array.isArray(e) ? `[…] ${e.length} items` : `{…} ${Mr(e)} keys`;
function Vr(e, t, n) {
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
function Hr(e, t, n, r) {
	let i = t.applied.trim(), a = null;
	if (i) {
		let n = t.memo;
		a = n && n.root === e && n.q === i ? n : t.memo = {
			root: e,
			q: i,
			...Lr(e, i)
		};
	}
	let o = t.auto;
	(!o || o.root !== e) && (o = t.auto = {
		root: e,
		open: Ir(e)
	});
	let s = o.open, c = (e) => () => {
		e(), t.said = "", r.redraw();
	}, l = (e) => t.open.get(e) ?? (a?.via.has(e) || s.has(e)), u = [], d = (e, n, i, o) => {
		let s = K(n), d = s && l(i), p = F("div", `weft-tn${a?.hits.has(i) ? " weft-hit" : ""}`, void 0, { "data-key": `n${i}` });
		if (p.style.paddingLeft = `${o * 12}px`, s) {
			let n = F("button", "weft-tt", d ? "▾" : "▸", {
				type: "button",
				"aria-expanded": String(d),
				"aria-label": `${d ? "collapse" : "expand"} ${e}`
			});
			I(n, "click", c(() => t.open.set(i, !d))), p.appendChild(n);
		} else p.appendChild(F("span", "weft-tt", "", { "aria-hidden": "true" }));
		if (p.appendChild(F("span", "weft-tk", `${e}: `)), s) p.appendChild(F("span", "weft-tv", Br(n)));
		else if (typeof n == "string" && n.length > 2048 && !t.full.has(i)) {
			let e = zr(n);
			p.appendChild(F("span", "weft-tv", `${JSON.stringify(n.slice(0, e)).slice(0, -1)}…`));
			let r = t.bytes.get(i);
			r?.v !== n && t.bytes.set(i, r = {
				v: n,
				n: Rr(n, e)
			});
			let a = F("button", "weft-btn weft-tmore", `… +${r.n} bytes`, {
				type: "button",
				title: "show the whole string"
			});
			I(a, "click", c(() => t.full.add(i))), p.appendChild(a);
		} else p.appendChild(F("span", "weft-tv", Fr(n, 0)));
		let m = F("button", "weft-tc", "⧉", {
			type: "button",
			title: "copy this node's JSON",
			"aria-label": `copy ${e}`
		});
		I(m, "click", () => Vr(t, Fr(n), r)), p.appendChild(m), u.push(p), d && f(n, i, o + 1);
	}, f = (e, n, r) => {
		let i = 200 + (t.more.get(n) ?? 0), o = 0;
		if (Nr(e, (e, t, s) => {
			let c = Pr(n, e);
			s < i || a?.via.has(c) || a?.hits.has(c) ? d(e, t, c, r) : o++;
		}), o) {
			let e = F("button", "weft-btn weft-tmore", `… +${o} more`, {
				type: "button",
				"data-key": `m${n}`
			});
			e.style.marginLeft = `${r * 12 + 16}px`, I(e, "click", c(() => t.more.set(n, (t.more.get(n) ?? 0) + 200))), u.push(e);
		}
	};
	K(e) ? f(e, "", 0) : d("value", e, "", 0);
	let p = F("div", "weft-tbar", void 0, { "data-key": "tbar" }), m = F("input", "weft-input weft-tree-q", void 0, {
		type: "search",
		"aria-label": "filter keys and values",
		placeholder: "/ filter keys and values"
	});
	m.value = t.q, I(m, "input", (e, n) => {
		t.q = n.value, clearTimeout(t.timer), t.timer = setTimeout(() => {
			t.applied = t.q, t.open.clear(), t.said = "", r.redraw();
		}, 100);
	}), p.appendChild(m);
	let h = a ? `${a.n} match${a.n === 1 ? "" : "es"}${a.n > a.hits.size ? ` · first ${a.hits.size} opened` : ""}` : "";
	p.appendChild(F("span", "weft-tree-n", h));
	let g = F("button", "weft-btn", "copy all", {
		type: "button",
		title: "copy the whole document's JSON"
	});
	I(g, "click", () => Vr(t, Fr(e), r)), p.appendChild(g);
	let _ = F("a", "weft-btn weft-dl", "download", {
		href: "#",
		download: `${n}.json`,
		title: `save as ${n}.json`
	});
	I(_, "click", (t, n) => {
		let r = Fr(e);
		try {
			let e = URL.createObjectURL(new Blob([r], { type: "application/json" }));
			setTimeout(() => URL.revokeObjectURL(e), 6e4), n.setAttribute("href", e);
		} catch {
			n.setAttribute("href", `data:application/json;charset=utf-8,${encodeURIComponent(r)}`);
		}
	}), p.appendChild(_), t.said && p.appendChild(F("span", "weft-tree-said", t.said));
	let v = F("div", "weft-raw", [p], { "data-key": "raw" });
	if (t.box) {
		let e = F("textarea", "weft-input weft-copybox", void 0, {
			readonly: "",
			"aria-label": "the JSON to copy",
			rows: "4"
		});
		e.value = t.box;
		let n = () => {
			t.box = "", t.said = "", r.redraw();
		};
		I(e, "blur", n), I(e, "keydown", (e) => {
			e.key === "Escape" && (e.preventDefault(), n());
		}), v.appendChild(e);
	}
	let y = F("div", "weft-tree", u, { "data-key": "tree" });
	return I(y, "keydown", (e) => {
		let t = e.key, n = e.target;
		t !== "ArrowRight" && t !== "ArrowLeft" || !n.classList.contains("weft-tt") || n.getAttribute("aria-expanded") === "true" == (t === "ArrowLeft") && (e.preventDefault(), n.click());
	}), v.appendChild(y), v;
}
//#endregion
//#region src/lib/request-pane.ts
var Ur = "changed by PrepareStep", Wr = "overridden by experiment", Gr = "catalog changed at this step", Kr = "prompt changed at this step", qr = "weft.override.";
function Jr(e, t) {
	let n = e?.find((e) => e.attrs["gen_ai.operation.name"] === "invoke_agent" && e.attrs["weft.run.id"] === t);
	if (!n) return;
	let r = [], i;
	for (let [e, t] of Object.entries(n.attrs)) {
		if (!e.startsWith(qr)) continue;
		let n = e.slice(14);
		n === "hash" ? i = typeof t == "string" ? t : String(t) : r.push(n);
	}
	if (!i && r.length === 0) return;
	let a = n.attrs[`${qr}instructions`];
	return {
		hash: i,
		instructions: a === !0 || a === "true",
		fields: r.sort()
	};
}
function Yr(e, t, n) {
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
async function Xr(e) {
	let t = globalThis.crypto?.subtle;
	if (!t) return;
	let n = await t.digest("SHA-256", new TextEncoder().encode(e));
	return Array.from(new Uint8Array(n), (e) => e.toString(16).padStart(2, "0")).join("");
}
function Zr(e, t) {
	let n = e;
	for (let e of t) e && (n.length > 0 && (n += "\n\n"), n += e);
	return n;
}
function Qr(e, t) {
	if (!t) return;
	let n = new Map(t.tools.map((e) => [e.name, e.prompt_snippet ?? ""]));
	if (!e.some((e) => !n.has(e))) return e.map((e) => n.get(e) ?? "");
}
function $r(e) {
	let t = e?.prompt;
	return !e || !t || Fe(t) ? e && !e.system_hash ? "" : void 0 : t.content === "derived" ? void 0 : t.text;
}
async function ei(e, t, n) {
	let r = e.system_hash;
	if (r === t || r === "" && t === "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") return !0;
	let i = $r(e);
	if (i === void 0) return;
	if (n === void 0) {
		if (e.body.tools.names.length > 0) {
			let e = await Xr(i);
			return e === void 0 || e !== t ? void 0 : !0;
		}
		n = [];
	}
	let a = n.filter(Boolean);
	if (a.length === 0) {
		let e = await Xr(i);
		return e === void 0 ? void 0 : e === t;
	}
	let o = a.join("\n\n");
	if (t === "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") return i === o;
	if (!i.endsWith(`\n\n${o}`)) return !1;
	let s = await Xr(i.slice(0, i.length - o.length - 2));
	return s === void 0 ? void 0 : s === t;
}
function ti(e) {
	return e.kind === "previous" ? `diff vs step ${e.step}` : e.verified ? e.overridden ? "diff vs the registered instructions (overridden for this run)" : "diff vs the registered instructions" : "diff vs weft.json's instructions — not verified for this run";
}
function ni(e, t, n) {
	let r = new Set(e), i = new Set(t), a = [...e.filter((e) => !i.has(e)), ...t.filter((e) => !r.has(e))];
	if (a.length === 0) {
		if (!(e.length !== t.length || e.some((e, n) => e !== t[n]))) return !1;
		if (!n?.verified) return !0;
		let r = Qr(t, n.agent);
		return r === void 0 || r.some(Boolean);
	}
	if (!n?.verified) return !0;
	let o = Qr(a, n.agent);
	return o === void 0 || o.some(Boolean);
}
function ri(e) {
	let t = [...e].sort((e, t) => e.step - t.step || e.index - t.index), n = /* @__PURE__ */ new Map(), r;
	for (let e of t) r && r.step !== e.step && !n.has(e.step) && n.set(e.step, r), (!r || r.step !== e.step || e.index > r.index) && (r = e);
	return n;
}
function ii(e, t, n = [], r = 3, i = !1) {
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
function ai(e) {
	return e < 1024 ? `${e} B` : e < 1048576 ? `${(e / 1024).toFixed(1)} KB` : `${(e / 1048576).toFixed(1)} MB`;
}
//#endregion
//#region src/panel/request.ts
var oi = 12, q = (e, t) => F("div", "weft-rq-hole", [L(e, t)], { "data-weft-rq-hole": e }), si = (e, t) => q(e, e === "not_recorded" ? { label: Ye } : e === "stripped" ? t.t.requests?.stripped : void 0), ci = (e, t) => F("div", "weft-rq-hole", R(e, t), { "data-weft-rq-hole": e }), J = (e, t, n) => F("div", "weft-rq-row", [F("span", "weft-rq-k", e), F("div", "weft-rq-v", t)], {
	"data-key": n,
	"data-weft-rq": n
}), Y = (e, t = "weft-res") => F("span", t, e), li = (e, t) => F("span", "weft-badge weft-info", t, { "data-weft-mark": e });
function ui(e, t, n, r) {
	let i = F("div", "weft-holes weft-rq-pick", void 0, {
		role: "group",
		"aria-label": e,
		"data-key": e
	});
	for (let a of t) {
		let t = F("button", `weft-btn${a === n ? " weft-active" : ""}`, `${e} ${a}`, {
			type: "button",
			"aria-pressed": String(a === n),
			[`data-weft-rq-${e}`]: String(a)
		});
		I(t, "click", () => r(a)), i.appendChild(t);
	}
	return i;
}
var di = /* @__PURE__ */ new Map();
function fi(e, t) {
	di.set(e, t), di.size > 64 && di.delete(di.keys().next().value);
}
function pi(e, t, n, r) {
	let i = [
		e.system_hash,
		t,
		e.body.tools.names.join(","),
		n?.join("\0") ?? ""
	].join("|"), a = di.get(i);
	if (a !== "pending") {
		if (a !== void 0) return a ?? void 0;
		fi(i, "pending"), ei(e, t, n).then((e) => {
			fi(i, e ?? null), r();
		}, () => fi(i, null));
	}
}
function mi(e) {
	let t = e?.prompt;
	return !!t && !Fe(t) && t.truncated_bytes > 0;
}
function hi(e, t, n, r, i) {
	let a = e.t.doc, o = n === void 0, s = a?.instructions_hash, c = Jr(e.t.spans ?? void 0, e.t.id), l = o && c?.instructions === !0, u = o && !!s && i.system_hash !== s, d = !o && (t.steps.get(r)?.promptChanged ?? !1), f = i.body.tools.names, p = n?.body.tools.names ?? [], m = d && (f.length !== p.length || f.some((e, t) => e !== p[t])), h = u || l || m, g = h ? e.manifest() : void 0, _ = !h || g !== void 0, v = a?.agent ? Yr(g ?? void 0, a.agent, a.manifest_hash || void 0) : void 0, y = Qr(f, v?.agent), b = v?.verified ? y : void 0, x = $r(i);
	if (!o) {
		let e = $r(n);
		return {
			prompt: d ? m && (!_ || ni(p, f, v)) ? "either" : "prepare_step" : void 0,
			experiment: !1,
			noManifest: !1,
			base: d && e !== void 0 && x !== void 0 ? {
				kind: "previous",
				step: n.step,
				text: e,
				truncated: mi(n)
			} : void 0
		};
	}
	let S = u && _ ? pi(i, s, b, e.redraw) : void 0, C = v && v.agent.instructions !== void 0 && y ? Zr(v.agent.instructions, y) : void 0;
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
var gi = /* @__PURE__ */ new WeakMap();
function _i(e, t, n) {
	let r = F("div", "weft-diff", [F("div", "weft-diff-h", ti(t))], { "data-weft-prompt-diff": t.kind });
	if (mi(e) || t.truncated) return r.appendChild(q("truncated", { label: et })), r;
	let i = gi.get(e);
	(!i || i.base !== t.text) && gi.set(e, i = {
		base: t.text,
		diff: bn(t.text, n)
	});
	let a = i.diff;
	if ("tooLarge" in a) return r.appendChild(F("div", "weft-reason", `too large to diff (${a.tooLarge.before} → ${a.tooLarge.after} lines)`, { "data-weft-diff-too-large": "" })), r;
	for (let e of a.rows) r.appendChild(F("div", `weft-diff-row weft-diff-${e.kind}`, `${vi[e.kind]}${e.text}`, { "data-weft-diff": e.kind }));
	return r;
}
var vi = {
	add: "+ ",
	del: "− ",
	same: "  "
};
function yi(e, t, n) {
	let r = e.keys.has(t), i = F("button", "weft-btn weft-rq-more", n(r), {
		type: "button",
		"aria-expanded": String(r)
	});
	return I(i, "click", () => {
		e.keys.has(t) ? e.keys.delete(t) : e.keys.add(t), e.redraw();
	}), [i, r];
}
function bi(e, t, n, r) {
	let i = t.prompt;
	if (!t.system_hash) return [Y("no system prompt")];
	if (!i || Fe(i)) return [Y(M(t.system_hash), "weft-rq-hash"), ...i ? [si(i.badge, e)] : []];
	if (i.content === "derived") return [Y(M(i.hash), "weft-rq-hash"), q("derived")];
	let a = i.text.split("\n"), o = a.length > oi, [s, c] = o ? yi(e, `${r}\u0000rq-prompt`, (e) => e ? "show less" : `show all ${a.length} lines`) : [null, !0], l = [F("div", "weft-res weft-rq-prompt", o && !c ? `${a.slice(0, oi).join("\n")}\n…` : i.text, { "data-weft-prompt": "" })], u = F("div", "weft-holes", [Y(M(i.hash), "weft-rq-hash")]);
	return s && u.appendChild(s), l.push(u), i.truncated_bytes > 0 && l.push(q("truncated", { bytes: i.truncated_bytes })), n.base && n.base.text !== i.text && l.push(_i(t, n.base, i.text)), l;
}
function xi(e) {
	let t = F("div", "weft-holes");
	for (let n of [
		`timeout ${e.timeout_ms > 0 ? `${e.timeout_ms}ms` : "none"}`,
		`approval ${e.approval ? "required" : "no"}`,
		`replay ${e.replay || "never"}`,
		`result cap ${e.max_result_bytes > 0 ? ai(e.max_result_bytes) : "off"}`,
		e.sequential ? "sequential" : "",
		`source ${e.source || "?"}`
	]) n && t.appendChild(F("span", "weft-chip", n));
	return t;
}
function Si(e, t, n, r) {
	let i = t.tools, a = t.body.tools.names;
	if (!t.catalog_hash) return [Y("no tools offered")];
	if (!i || Fe(i)) {
		let n = [];
		return a.length && n.push(Y(a.join(", "))), n.push(i ? si(i.badge, e) : Y(M(t.catalog_hash), "weft-rq-hash")), n;
	}
	if (i.content === "derived") return [Y(M(i.hash), "weft-rq-hash"), q("derived")];
	let o = F("div", "weft-rq-tools");
	for (let t of i.tools) {
		let [i, a] = yi(e, `${n}\u0000rq-tool\u0000${t.name}`, (e) => `${e ? "▾" : "▸"} ${t.name}`);
		i.className = "weft-btn weft-rq-tool";
		let s = F("div", void 0, [i], {
			"data-key": `tool:${t.name}`,
			"data-weft-tool": t.name
		});
		a && (s.appendChild(F("div", "weft-res", t.description)), s.appendChild(xi(t)), s.appendChild(Hr(t.schema, e.tree(`schema\u0000${r}\u0000${t.name}`), `${t.name}-schema`, e.cx))), o.appendChild(s);
	}
	let s = [o, F("div", "weft-holes", [Y(M(i.hash), "weft-rq-hash")])];
	return i.truncated_bytes > 0 && s.push(q("truncated", { bytes: i.truncated_bytes })), s;
}
var Ci = /* @__PURE__ */ new WeakMap();
function wi(e, t, n) {
	let r = Tn(e.t.doc), i = Ci.get(t);
	(!i || i.tr !== e.t.transcript || i.comps !== e.t.doc) && Ci.set(t, i = {
		tr: e.t.transcript,
		comps: e.t.doc,
		m: ii(t, e.t.transcript, r)
	});
	let a = i.m, o = `${a.count} ${a.count === 1 ? "message" : "messages"}`, s = [F("div", "weft-res", a.bytes === void 0 ? o : `${o} · ${ai(a.bytes)}`, { "data-weft-messages-line": "" })];
	if (a.hole === "no_transcript" ? s.push(F("div", "weft-reason", "bytes when the transcript is read")) : a.hole && s.push(a.hole === "compacted" || a.hole === "no_index" && t.content === "stripped" ? q(a.hole === "compacted" ? a.hole : "stripped") : q("gap", { reason: a.hole === "gap" ? Qe : $e })), a.last.length) {
		let e = F("div", void 0, void 0, { "data-weft-messages-last": "" });
		for (let t of a.last) e.appendChild(F("div", "weft-res", An(t)));
		s.push(e);
	}
	if (a.before.length) {
		let [t, r] = yi(e, `${e.t.id}\u0000rq-earlier\u0000${n}`, (e) => `${e ? "▾" : "▸"} ${a.before.length} earlier ${a.before.length === 1 ? "message" : "messages"} (raw)`);
		s.push(t), r && s.push(F("div", void 0, [Hr(a.before, e.tree(`earlier\u0000${n}`), `step${n}-messages`, e.cx)], { "data-weft-messages-earlier": "" }));
	}
	return s;
}
function Ti(e, t) {
	let n = new Set(e.folded.steps.map((e) => e.index));
	for (let e of t?.steps.keys() ?? []) n.add(e);
	return [...n].sort((e, t) => e - t);
}
function Ei(e) {
	let t = e.t, n = t.requests, r = F("div", "weft-rq");
	if (n?.badge) return r.appendChild(ci(n.badge, {
		reason: n.reason,
		fix: n.fix
	})), r;
	if (!n) return r.appendChild(t.doc?.requests_badge ? ci(t.doc.requests_badge) : ci("not_recorded", {
		cause: "not_served",
		reason: e.notServed
	})), r;
	if (n.error) return r.appendChild(F("span", "weft-badge weft-err", `request could not be read: ${n.error}`)), r;
	let i = Ti(t, n), a = e.step !== void 0 && i.includes(e.step) ? e.step : i[0] ?? 0;
	r.appendChild(ui("step", i, a, e.select));
	let o = F("div", "weft-rq-pane", void 0, {
		"data-key": `rq${a}`,
		"data-weft-rq-pane": String(a)
	});
	r.appendChild(o);
	let s = Tn(t.doc);
	for (let t of s) (H(t) ? a === i[0] : t.step === a) && o.appendChild(e.compaction(t));
	let c = n.steps.get(a), l = c?.rows ?? [];
	if (!l.length) return o.appendChild(e.running ? F("div", "weft-reason", tt) : n.truncated ? Nt(10 * en) : q("gap", { reason: Xe })), r;
	let u = ri([...n.steps.values()].flatMap((e) => e.rows)), d = l[0], f = e.pick.get(a), p = l.find((e) => e.attempt === f) ?? l[l.length - 1], m = hi(e, n, u.get(a), a, d), h = F("div", "weft-holes weft-rq-head", void 0, { "data-key": "head" }), g = [p.body.model.provider, p.body.model.name].filter(Boolean).join("/");
	g && h.appendChild(Y(g));
	let _ = t.folded.steps.find((e) => e.index === a), v = Nn(Mn(l, !!_?.finish || (_?.toolCalls.length ?? 0) > 0, e.running));
	v && h.appendChild(F("span", "weft-badge weft-info", v, { "data-weft-attempts": "" })), m.prompt && h.appendChild(li("prompt", m.prompt === "either" ? Kr : Ur)), m.experiment && h.appendChild(li("experiment", Wr)), c?.catalogChanged && h.appendChild(li("catalog", Gr)), o.appendChild(h), l.length > 1 && o.appendChild(ui("attempt", l.map((e) => e.attempt), p.attempt, (t) => {
		e.pick.set(a, t), e.redraw();
	})), p.content && o.appendChild(q(p.content, p.content === "stripped" ? n.stripped : void 0)), m.noManifest && o.appendChild(F("div", "weft-reason", "no manifest readable here: no diff vs the registered instructions", { "data-weft-no-manifest": "" }));
	let y = `${t.id}\u0000${a}`, b = p.body, x = m.base && p.system_hash === d.system_hash ? m : {
		...m,
		base: void 0
	};
	o.appendChild(J("system", bi(e, p, x, y), "system")), o.appendChild(J("messages", wi(e, p, a), "messages")), o.appendChild(J("tools", Si(e, p, y, a), "tools"));
	let S = F("div", "weft-rq-params");
	for (let [e, t] of qe(p)) S.appendChild(F("div", void 0, [F("span", "weft-rq-pk", e), F("span", t === "adapter default" ? "weft-rq-def" : "weft-res", t)], { "data-weft-param": e }));
	o.appendChild(J("params", [S], "params")), o.appendChild(J("tool choice", [Y(b.tool_choice ? `${b.tool_choice.mode}${b.tool_choice.name ? ` (${b.tool_choice.name})` : ""}` : "adapter default")], "tool_choice")), o.appendChild(J("thinking", [Y(b.thinking ? `${b.thinking.level}${b.thinking.budget ? ` · budget ${b.thinking.budget}` : ""}` : "adapter default")], "thinking"));
	let C = _?.toolCalls.filter((e) => e.childRunId) ?? [];
	return C.length && o.appendChild(J("subagents", C.map((t) => e.child(t)), "subagents")), r;
}
//#endregion
//#region src/lib/links.ts
function Di(e) {
	return typeof e == "number" && Number.isInteger(e) && e >= 0 ? e : void 0;
}
function Oi(e, t = {}) {
	let n = Di(t.step), r = {};
	n !== void 0 && (r.step = n), t.view && t.view !== "trace" && (r.view = t.view), t.call && (t.resumed || n !== void 0) ? r.sel = `c:${t.resumed ? "resume" : String(n)}:${t.call}` : t.span && (r.sel = `t:${t.span}`), t.axis === "time" ? r.axis = "time" : t.axis === "events" && (r.axis = "events");
	let i = Di(t.t);
	return i !== void 0 && (r.t = i), {
		to: "/runs/$id",
		params: { id: e },
		search: r
	};
}
function ki(e) {
	return {
		to: "/sessions/$id",
		params: { id: e },
		search: {}
	};
}
function Ai(e, t = {}) {
	return {
		to: "/traces/$id",
		params: { id: e },
		search: t.span ? { span: t.span } : {}
	};
}
function ji(e = {}) {
	let t = new URLSearchParams();
	for (let [n, r] of Object.entries(e)) r !== void 0 && r !== "" && (n !== "step" || Di(r) !== void 0) && t.set(n, String(r));
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
function Mi(e) {
	if (typeof e == "string") try {
		return JSON.parse(e), JSON.stringify(e);
	} catch {
		return e;
	}
	return typeof e == "object" ? JSON.stringify(e) : String(e);
}
function Ni(e) {
	let t = e.to.replace(/^\//, "");
	"params" in e && (t = t.replace("$id", encodeURIComponent(e.params.id)));
	let n = new URLSearchParams();
	for (let [t, r] of Object.entries(e.search)) r !== void 0 && n.set(t, Mi(r));
	let r = n.toString(), i = "hash" in e && e.hash ? `#${e.hash}` : "";
	return `${t}${r ? `?${r}` : ""}${i}`;
}
function X(e, t) {
	return new URL(Ni(t), e).toString();
}
//#endregion
//#region src/panel/element.ts
function Pi(e, t) {
	return e.meta?.capabilities.includes(t) ?? !1;
}
function Z(e) {
	return e.status === "succeeded" && e.pending > 0 ? "parked" : e.status;
}
function Fi(e, t, n) {
	return X(e, Oi(t, n === void 0 ? {} : {
		step: n,
		view: "story"
	}));
}
var Ii = null;
function Li(e) {
	try {
		if ("adoptedStyleSheets" in e && typeof CSSStyleSheet == "function") {
			if (!Ii) {
				let e = new CSSStyleSheet();
				e.replaceSync(Jn), Ii = e;
			}
			e.adoptedStyleSheets = [Ii];
			return;
		}
	} catch {}
	e.append(F("style", void 0, Jn));
}
var Q = () => {}, Ri = 3e3;
function $(e) {
	return r({
		publicId: e.publicId,
		session: e.session,
		flow: e.flow
	});
}
function zi(e, t, n) {
	e.add(t), e.size > n && e.delete(e.values().next().value);
}
function Bi(e, t, n, r) {
	e.delete(t), e.set(t, n), e.size > r && e.delete(e.keys().next().value);
}
var Vi = /* @__PURE__ */ new Set(), Hi = [
	"data-scope=\"pub_…\" on the panel's <script> tag (or <weft-devtools>)",
	"scope(\"pub_…\") from @weftgo/devtools",
	"data-weft-scope=\"pub_…\" on the chat's element",
	"scope.Header(h, …) on the app's handler (Go, package weft/scope)"
], Ui = "no conversation detected on this page";
function Wi() {
	try {
		return typeof window.matchMedia == "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
	} catch {
		return !1;
	}
}
function Gi(e) {
	let t = (typeof e.composedPath == "function" ? e.composedPath()[0] : null) ?? e.target;
	return !!t && (/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) || t.isContentEditable);
}
var Ki = [
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
], qi = [
	"run",
	"parked",
	"error"
], Ji = /* @__PURE__ */ new WeakSet(), Yi = null;
function Xi(e) {
	if (!e || typeof e != "object") return !1;
	let t = Object.getPrototypeOf(e);
	return t === Object.prototype || t === null;
}
var Zi = 1e3, Qi = 3;
function $i(e) {
	if (!e || typeof e != "object") return null;
	let t = e, n = (...e) => {
		for (let n of e) if (typeof t[n] == "string" && t[n]) return t[n];
		return "";
	}, r = n("session", "sessionId");
	if (typeof t.publicId != "string" && !r) return null;
	let i = { publicId: typeof t.publicId == "string" ? t.publicId : "" }, a = n("flow"), o = n("run", "runId");
	return r && (i.session = r), a && (i.flow = a), o && (i.run = o), i;
}
var ea = 1e4;
function ta(e) {
	let t = e.scope;
	return typeof t == "string" ? t : t ? r(t) : e.publicId ? r({ publicId: e.publicId }) : "";
}
var na = (e) => typeof e == "number" && Number.isInteger(e) && e >= 0 ? e : void 0, ra = class e extends HTMLElement {
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
	push = new re();
	themeWatch = new fe();
	themeNow = "dark";
	resolveNow() {
		return ue(this.cfg.theme, this.lay.theme, this.themeWatch.host);
	}
	placed = !1;
	dragging = !1;
	streamKey = "";
	said = "";
	rove = "";
	gAt = 0;
	tree = jr();
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
			this.resizeFrame = null, this.isConnected && (T(this.lay), this.render(this.last));
		};
		this.resizeFrame = typeof requestAnimationFrame == "function" ? requestAnimationFrame(e) : setTimeout(e, 16);
	};
	get shown() {
		return this.lay.open && !this.lay.hidden;
	}
	opened = !1;
	keys = !1;
	body;
	last = hr();
	held = !1;
	composing = !1;
	dirty = !1;
	holdTimer = null;
	scratch = /* @__PURE__ */ new Map();
	openKeys = /* @__PURE__ */ new Set();
	onKey = (e) => this.keydown(e);
	onURL = () => {
		let e = ke();
		(e ? r(e) : "") !== (this.cfg.urlScope ? r(this.cfg.urlScope) : "") && this.rescan();
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
	globalNote = "";
	static noGlobal = !1;
	constructor() {
		super(), this.cfg = D(this), this.lay = _(this.cfg.position, this.cfg.open, this.cfg.mode), this.shadow = this.attachShadow({ mode: "open" }), this.body = F("div", "weft-root"), Li(this.shadow), this.shadow.append(this.body), this.shadow.addEventListener("pointerdown", () => this.hold()), this.shadow.addEventListener("keydown", (e) => this.panelKey(e)), this.shadow.addEventListener("compositionstart", () => {
			this.composing = !0;
		}), this.shadow.addEventListener("compositionend", () => {
			this.composing = !1, this.flush();
		}), this.shadow.addEventListener("focusout", () => {
			this.composing && (this.composing = !1, this.flush());
		}), this.render(this.last);
	}
	connectedCallback() {
		if (this.cfg = D(this), !this.opened) {
			this.opened = !0;
			let e = y();
			this.placed = x(e), this.lay = {
				..._(this.cfg.position, this.cfg.open, this.cfg.mode),
				...e
			};
		}
		T(this.lay), Vi.add(this), window.addEventListener("keydown", this.onKey), window.addEventListener("resize", this.onResize, { passive: !0 }), window.addEventListener("pointerup", this.onRelease, !0), window.addEventListener("pointercancel", this.onRelease, !0), window.addEventListener("hashchange", this.onURL, { passive: !0 }), window.addEventListener("popstate", this.onURL, { passive: !0 }), this.themeWatch.start(() => {
			this.getAttribute("data-theme-resolved") !== this.resolveNow() && this.render(this.last);
		}), this.themeNow = this.resolveNow(), this.syncTheme(), this.syncGlobal(), this.schedule();
	}
	disconnectedCallback() {
		Vi.delete(this), window.removeEventListener("keydown", this.onKey), window.removeEventListener("resize", this.onResize), this.resizeFrame !== null && (typeof cancelAnimationFrame == "function" && cancelAnimationFrame(this.resizeFrame), clearTimeout(this.resizeFrame), this.resizeFrame = null), Ar(this.tree);
		for (let e of this.rqTrees.values()) Ar(e);
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
				let a = ta(i);
				a ? this.setAttribute("data-weft-scope", a) : this.removeAttribute("data-weft-scope"), this.rescan(), this.render(this.last);
				return;
			}
			let n = typeof e == "string" ? i(e) : $i(e);
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
		let t = r(e), n = this.options?.scope;
		if ((typeof n == "string" ? n : n ? r(n) : "") === t && this.getAttribute("data-weft-scope") === t) {
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
		if (O(this.cfg.token) !== "") {
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
				let e = await on({
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
					this.say(`session ${n} has no public id · ${a === "not_recorded" ? Ft(o.reason, o.fix) : "none recorded"}`);
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
			let r = na(t), i = ++this.selectSeq;
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
		if (!qi.includes(e) || typeof t != "function") return () => {};
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
			return Fi(n, e, na(t));
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
		return Ji.add(t), this.apiObj = t, t;
	}
	syncGlobal() {
		try {
			if (!(this.isConnected && this.cfg.global && !e.noGlobal)) {
				this.globalNote = "", this.dropGlobal();
				return;
			}
			let t = window, n = t.weft;
			if (n === void 0) Yi = {}, n = Yi, t.weft = n;
			else if (!Xi(n)) {
				this.globalNote = "window.weft is the page's: no window.weft.devtools";
				return;
			}
			let r = n, i = Object.getOwnPropertyDescriptor(r, "devtools");
			if (i ? !i.writable && !i.set : !Object.isExtensible(r)) {
				this.globalNote = "window.weft is the page's: no window.weft.devtools";
				return;
			}
			let a = r.devtools, o = a !== void 0 && !!a && typeof a == "object" && Ji.has(a);
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
			if (!Xi(t) || t.devtools !== this.apiObj || !this.apiObj) return;
			delete t.devtools, Array.from(document.querySelectorAll("weft-devtools")).find((e) => e !== this && e.isConnected && typeof e.syncGlobal == "function")?.syncGlobal(), !("devtools" in t) && t === Yi && Object.keys(t).length === 0 && (delete e.weft, Yi = null);
		} catch {}
	}
	say(e) {
		this.note = e, this.render(this.last);
	}
	afterSettle(e) {
		this.settled().then(e).catch(Q);
	}
	async settled() {
		let e = Date.now() + ea;
		for (let t = 0; t < 20; t++) {
			this.scheduled && await Promise.resolve();
			let t = this.settling, n = e - Date.now();
			if (n <= 0) return !1;
			let r, i = await Promise.race([t.catch(Q).then(() => !1), new Promise((e) => r = setTimeout(() => e(!0), n))]);
			if (clearTimeout(r), i) return !1;
			if (t === this.settling && !this.scheduled) return !0;
		}
		return !0;
	}
	watchRuns(e) {
		let t = this.model;
		if (!t || e !== t.state || !t.publicId || this.dormant) return;
		let n = Er(t.publicId, t.narrowing.session);
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
			let a = Z(n), o = this.evStatus.get(n.id);
			if (o === a || (this.evStatus.set(n.id, a), o === void 0 && r && a !== "running" && n.id !== i)) continue;
			let s = (e.turn?.id === n.id ? e.turn.folded.steps.at(-1)?.index : void 0) ?? (n.steps > 0 ? n.steps - 1 : void 0);
			this.fire("run", {
				runId: n.id,
				status: a,
				publicId: n.public_id || t.publicId,
				...n.session_id ? { sessionId: n.session_id } : {},
				...s === void 0 ? {} : { step: s }
			}), a === "failed" && !this.evErrored.has(n.id) && (zi(this.evErrored, n.id, 500), this.fire("error", {
				message: n.err || `run ${n.id} failed`,
				runId: n.id
			})), a === "parked" && this.reportParked(t, n.id, 1);
		}
	}
	reportParked(e, t, n) {
		this.evParking.has(t) || (this.evParking.add(t), e.pendingCalls(t).then((r) => {
			this.evParking.delete(t);
			let i = e.rowOf(t);
			if (this.model === e && i && Z(i) === "parked") {
				if (!r.length) {
					n < Qi && setTimeout(() => {
						this.model === e && this.reportParked(e, t, n + 1);
					}, Zi);
					return;
				}
				for (let e of r) {
					let n = `${t}\u0000${e.id}`;
					this.evParked.has(n) || (zi(this.evParked, n, 500), this.fire("parked", {
						runId: t,
						callId: e.id,
						ackId: e.id,
						name: typeof e.name == "string" ? e.name : ""
					}));
				}
			}
		}).catch(Q));
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
		return !!this.cfg.urlScope && !this.cfg.scopeExplicit && r(this.scopeNow()) === r(this.cfg.urlScope);
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
			let r = $(t);
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
		return [r(this.scopeNow()), ...this.conversations().map((e) => e.key + e.source)].join("|");
	}
	onMarkers(e) {
		let t = this.markers, n = (e) => r(e.scope);
		if (e.length === t.length && e.every((e, r) => e.element === t[r].element && n(e) === n(t[r]))) return;
		let i = this.markerSig(), a = this.marked ? $(this.marked) : "";
		a && e.length === 1 && !e.some((e) => $(e.scope) === a) && !e.some((e) => t.some((t) => t.element === e.element)) && (this.marked = null), this.markers = e, this.follow(), this.markerSig() !== i && this.rescopeSoon();
	}
	onMarkerFocus(e) {
		if (e === this.lastFocused && !this.chosen) return;
		let t = this.markerSig();
		this.lastFocused = e, this.chosen = null, this.follow(), this.markerSig() !== t && this.rescopeSoon();
	}
	follow() {
		let e = this.markers, t = (t) => t ? e.find((e) => e.element === t) : void 0, n = this.marked ? $(this.marked) : "", r = t(Vn(document.activeElement)) ?? t(this.lastFocused) ?? e.find((e) => n && $(e.scope) === n), i = $(this.cfg.scope);
		if (this.cfg.scopeExplicit && e.some((e) => $(e.scope) === i) && (this.explicitMarked = !0), r) this.marked = r.scope;
		else if (!this.marked) {
			let t = this.cfg.scopeExplicit ? $(this.cfg.scope) : "";
			this.marked = (t ? e.find((e) => $(e.scope) === t) : e.at(0))?.scope ?? null;
		}
	}
	dropMarkers() {
		this.markerRung?.disconnect(), this.markerRung = null, this.markers = [], this.marked = this.lastFocused = null;
	}
	detectedByPath() {
		return new Map(this.byPath);
	}
	syncDetect() {
		let e = this.isConnected && this.ready && !this.dormant && Ae(this.cfg);
		e && !this.rung ? (this.notRestored = !1, this.rung = u({
			onScope: (e, t) => this.onDetected(e, t),
			ignore: (e) => !!this.base && e.startsWith(this.base)
		})) : !e && this.rung && this.dropRung();
		let t = this.isConnected && !this.dormant && je(this.cfg);
		t && !this.markerRung ? this.markerRung = Wn({
			onScopes: (e) => this.onMarkers(e),
			onFocus: (e) => this.onMarkerFocus(e)
		}) : !t && this.markerRung && this.dropMarkers();
		let n = this.cfg.scopeExplicit ? r(this.cfg.scope) : "";
		n !== this.explicitForm && (this.explicitForm = n, this.chosen = this.marked = this.lastFocused = null, this.explicitMarked = !1, n && (this.forceNext = !0), this.follow());
		let i = this.cfg.urlScope ? r(this.cfg.urlScope) : "";
		i !== this.urlForm && (this.urlForm = i, i && !this.cfg.scopeExplicit && (this.chosen = null, this.forceNext = !0));
	}
	dropRung() {
		this.rung && (this.notRestored = !this.rung.restore(), this.rung = null, this.detected = null, this.byPath.clear(), this.seen = [], this.followedPath = "");
	}
	onDetected(e, t) {
		let n = $(e);
		Bi(this.byPath, t, e, 64);
		let i = Date.now(), a = this.seen.find((e) => e.key === n);
		a ? (a.scope = e, a.path = t, a.at = i) : (this.seen.push({
			scope: e,
			key: n,
			path: t,
			at: i
		}), this.seen.length > 20 && this.seen.shift());
		let o = this.detected;
		if (o && $(o) !== n && this.followedPath && t !== this.followedPath) return;
		(!o || $(o) !== n) && (this.followedPath = t);
		let s = o ? r(o) : "";
		this.detected = e, r(e) !== s && !this.cfg.scopeExplicit && this.schedule();
	}
	scopeNow() {
		if (this.chosen) return this.chosen;
		let e = this.cfg, t = this.markerRung ? this.marked : null;
		if (e.scopeExplicit && !(t && this.explicitMarked)) return e.scope;
		if (e.urlScope && !e.scopeExplicit) return e.urlScope;
		if (t && e.scopeExplicit && $(t) === $(e.scope)) return e.scope;
		let n = this.rung ? this.detected : null;
		return t ? n && $(n) === $(t) ? n : t : e.scopeExplicit ? e.scope : n ?? e.scope;
	}
	keydown(e) {
		e.defaultPrevented || e.isComposing || Gi(e) || this.ownsToggle() && (e.altKey && !e.ctrlKey && !e.shiftKey && !e.metaKey && e.code === "KeyW" || e.ctrlKey && e.shiftKey && !e.altKey && !e.metaKey && e.code === "KeyW") && (e.preventDefault(), this.keys = !1, this.toggle(), this.shown && this.body.querySelector(".weft-dock")?.focus({ preventScroll: !0 }));
	}
	ownsToggle() {
		try {
			let e = window.weft, t = Xi(e) ? Object.getOwnPropertyDescriptor(e, "devtools")?.value : void 0;
			if (t) {
				for (let e of Vi) if (e.apiObjIs(t)) return e === this;
			}
		} catch {}
		return Vi.values().next().value === this;
	}
	panelKey(e) {
		if (e.defaultPrevented || e.isComposing) return;
		if (e.key === "Tab") return this.trap(e);
		if (Gi(e)) return;
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
		let t = this.last, n = t.turn ? this.lay.tab === "request" ? Ti(t.turn, t.turn.requests) : t.turn.folded.steps.map((e) => e.index) : [];
		if (!n.length) return;
		let r = n.indexOf(this.stepNow(t) ?? -1), i = r < 0 ? e > 0 ? 0 : n.length - 1 : Math.min(Math.max(r + e, 0), n.length - 1);
		this.model?.selectStep(n[i]);
	}
	stepNow(e) {
		let t = aa(e);
		return t !== void 0 || this.lay.tab !== "request" || !e.turn || e.turn.id !== e.selected ? t : Ti(e.turn, e.turn.requests)[0];
	}
	openInStudio() {
		let e = this.last.selected ? this.studioLink(this.last.selected, this.stepNow(this.last)) : "";
		try {
			e && window.open(e, "_blank", "noopener");
		} catch {}
	}
	cycle() {
		let e = this.lay, t = d[(d.indexOf(e.mode === "float" ? "float" : e.side) + 1) % d.length];
		t === "float" ? e.mode = "float" : (e.mode = "dock", e.side = t), T(e), this.save(!0), this.render(this.last);
	}
	save(e = !1) {
		this.placed ||= e, S(this.lay, this.placed);
	}
	quiet() {
		return !(this.model?.drawPending() ?? !1);
	}
	rescan() {
		this.cfg = D(this), this.isConnected && (this.syncGlobal(), this.syncDetect(), this.schedule());
	}
	attributeChangedCallback(e, t, n) {
		if (t !== n && (this.cfg = D(this), this.isConnected)) {
			if (e === "data-position") {
				let e = _(this.cfg.position, !0, ""), t = this.lay;
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
			let n = this.scopeNow(), i = r(n);
			if (t.scope !== i) {
				this.model.publicId !== n.publicId && (this.scratch.clear(), this.prompts.clear()), t.scope = i;
				let r = this.forceNext || e.scopeExplicit && n === e.scope && !this.explicitMarked;
				this.settling = this.model.rescope(n, { force: r }).catch(Q);
			}
			this.forceNext = !1, this.render(this.last);
			return;
		}
		this.settling = this.start().catch(Q);
	}
	async start(e = !1) {
		let t = ++this.startSeq, n = this.cfg;
		this.ready = !1, this.dropRung(), this.model?.dispose(), this.model = null, this.conn = null, this.probe?.abort(), this.probe = null, this.scratch.clear(), this.byPath.clear(), this.prompts.clear(), this.evParked.clear(), this.evErrored.clear(), e && this.dormant && this.unreachable !== null ? (this.checking = !0, this.render(hr())) : (this.dormant = !1, this.unreachable = null, this.checking = !1, this.render(hr()));
		let i = !1, a = n.endpoint;
		if (n.configURL) {
			let e = new AbortController();
			this.probe = e;
			let r = setTimeout(() => e.abort(), Ri);
			try {
				a = await we(n.configURL, e.signal) || n.endpoint;
			} finally {
				clearTimeout(r), this.probe === e && (this.probe = null);
			}
			if (t !== this.startSeq) return;
		}
		if (a) {
			let e = this.scopeNow();
			this.forceNext = !1;
			let o = new Cr({
				base: a,
				token: n.token
			}, e, (e) => this.render(e));
			o.prefer = this.lay.run, this.model = o, this.conn = {
				endpoint: n.endpoint,
				token: n.token,
				scope: r(e)
			}, this.base = a, this.last = o.state;
			try {
				i = await o.start();
			} catch {
				i = !1;
			}
			if (t !== this.startSeq || this.model !== o) return;
		}
		if (this.checking = !1, i) {
			this.ready = !0, this.dormant && (this.dormant = !1, this.unreachable = null), this.syncDetect(), r(this.scopeNow()) !== this.conn?.scope && this.schedule(), this.render(this.last);
			return;
		}
		this.dropRung(), this.dropMarkers(), this.model?.dispose(), this.model = null, this.conn = null, this.autoMounted ? this.remove() : (this.dormant = !0, this.unreachable = a, this.render(this.last));
	}
	retry() {
		this.cfg = D(this), this.settling = this.start(!0).catch(Q);
	}
	toggle() {
		this.lay.hidden ? (this.lay.hidden = !1, this.lay.open = !0) : this.lay.open = !this.lay.open, this.save(!0), this.render(this.last);
	}
	toggleRaw() {
		this.setTab(this.lay.tab === "raw" ? this.prevTab : "raw");
	}
	prevTab = "story";
	setTab(e) {
		f.includes(e) && (this.lay.tab !== "raw" && (this.prevTab = this.lay.tab), this.lay.tab = e, this.lay.raw = e === "raw", this.save(), this.render(this.last));
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
				let e = F("div", "weft-unreachable", void 0, { role: "status" });
				if (e.append(this.unreachable ? "Studio not reachable at " : "Studio not reachable: no http(s) endpoint", ...this.unreachable ? [F("span", "weft-unreachable-at", this.unreachable)] : [], " · "), this.checking) e.append(F("span", "weft-checking", "checking…"));
				else {
					let t = F("button", "weft-retry", "retry", {
						type: "button",
						title: "ask Studio again"
					});
					I(t, "click", () => this.retry()), e.appendChild(t);
				}
				n.push(e);
			}
		} else this.lay.hidden || (this.shown ? e.gone || n.push(this.dock(e)) : n.push(this.pill(e)));
		let r = !!this.shadow.activeElement;
		Ct(t, n), r && !this.shadow.activeElement && t.querySelector(".weft-dock, .weft-fab")?.focus({ preventScroll: !0 }), this.watchOlder(), this.syncTheme();
		let i = this.dormant ? "line" : this.lay.hidden ? "hidden" : this.shown ? this.lay.mode : "pill";
		t.getAttribute("data-mode") !== i && t.setAttribute("data-mode", i), this.cfg.push && this.isConnected && t.querySelector(".weft-docked") ? this.push.apply(this.lay.side, `${this.lay.d}px`) : this.cfg.push && this.isConnected && t.querySelector(".weft-sheet") ? this.push.apply("bottom", "70vh") : this.push.restore();
	}
	syncTheme() {
		let e = this.themeNow;
		this.isConnected && this.getAttribute("data-theme-resolved") !== e && this.setAttribute("data-theme-resolved", e);
	}
	themeButton() {
		let e = ie(this.lay.theme), t = this.themeNow, n = this.cfg.theme !== "auto", r = n ? `theme: ${t}, set by the page` : `theme: ${e}${e === "auto" ? ` (${t})` : ""}`, i = F("button", "weft-btn weft-theme", "◐", {
			type: "button",
			title: n ? r : `${r} — next: ${de(e)}`,
			"aria-label": r,
			...n ? { "aria-disabled": "true" } : {}
		});
		return I(i, "click", () => {
			if (n) return;
			let t = de(e);
			this.lay.theme = t === "auto" ? "" : t, S(this.lay, this.placed), this.render(this.last);
		}), i;
	}
	dock(e) {
		T(this.lay);
		let t = ee(this.lay), n = F("div", `weft-dock weft-open ${t.cls}`, void 0, {
			tabindex: "-1",
			role: "complementary",
			"aria-label": "weft devtools",
			"data-key": "dock"
		});
		this.place(n, t);
		let r = this.header(e), i = this.lay;
		i.mode === "float" && !t.sheet && (r.classList.add("weft-drag"), I(r, "pointerdown", (e) => {
			let { x: t, y: n } = i;
			this.grab(e, (e, r) => {
				i.x = t + e, i.y = n + r;
			});
		})), n.appendChild(r), this.howTo && !this.model?.publicId && n.appendChild(this.howToBox());
		let a = F("div", "weft-cols");
		if (t.width < 640) {
			let t = this.turnPick(e);
			t && a.appendChild(t);
		}
		if (a.appendChild(this.turnList(e)), a.appendChild(this.main(e)), n.appendChild(a), n.appendChild(this.footer(e)), this.keys && n.appendChild(this.shortcuts()), n.appendChild(this.announcer(e)), !t.sheet) {
			let e = i.mode === "float", t = F("div", e ? "weft-grip" : `weft-edge weft-edge-${i.side}`, void 0, {
				title: "resize",
				"aria-hidden": "true"
			});
			I(t, "pointerdown", (t) => {
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
	place(e, t = ee(this.lay)) {
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
			t(n.clientX - i, n.clientY - a), T(this.lay), this.place(r), this.cfg.push && r.classList.contains("weft-docked") && this.push.apply(this.lay.side, `${this.lay.d}px`);
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
		return this.streamKey = r, F("div", "weft-sr", this.said, {
			role: "status",
			"data-key": "said"
		});
	}
	turnPick(e) {
		let t = [...e.turns, ...[...e.experiments.values()].flat()].filter((t) => t.id === e.selected || this.match(t));
		if (!t.length) return null;
		let n = F("select", "weft-turn-pick", void 0, {
			"aria-label": "turn",
			"data-weft-k": "turnpick"
		});
		for (let r of t) {
			let t = F("option", void 0, `${Z(r)} · ${r.id} · ${r.steps} steps`, {
				value: r.id,
				"data-key": r.id
			});
			t.selected = r.id === e.selected, n.appendChild(t);
		}
		return I(n, "change", (e, t) => this.go(this.model?.select(t.value, !0))), n;
	}
	pill(e) {
		let t = (t) => !!this.model?.publicId || t.agent === e.devAgent, n = e.live ? e.turns.find((e) => e.status === "running" && t(e)) : void 0, r = `weft-fab weft-fab-${te(this.lay)}`;
		if (!n) {
			let t = F("button", r, "devtools", { title: "weft devtools — Alt+W" });
			return this.pillEnd(t, e);
		}
		let i = e.turn?.id === n.id ? e.turn.folded.steps.length : 0, a = Math.max(i, n.steps), o = `weft devtools · running${a ? `, step ${a}` : ""}`, s = F("button", `${r} weft-fab-running${Wi() ? "" : " weft-fab-pulse"}`, [document.createTextNode("devtools"), F("span", "weft-fab-count", a ? ` ● ${a}` : " ●")], {
			title: `${o} — Alt+W`,
			"aria-label": o
		});
		return this.pillEnd(s, e, n);
	}
	pillEnd(e, t, n) {
		let r = n ?? [...t.turns, ...[...t.experiments.values()].flat()].find((e) => e.id === t.selected);
		if (r) {
			let t = We(r.status);
			e.appendChild(F("span", "weft-fab-cost", ` · ${t ? `${V(r.usage.input_tokens)}→${V(r.usage.output_tokens)}` : "—"}`, { title: t ? "the current turn's tokens, input→output" : "the current turn's usage: known when it finishes" }));
		}
		return n || e.setAttribute("aria-label", `weft ${e.textContent}`), this.cfg.zIndex && (e.style.zIndex = this.cfg.zIndex), I(e, "click", () => this.toggle()), e;
	}
	howToBox() {
		let e = F("div", "weft-howto");
		for (let t of Hi) e.appendChild(F("code", void 0, t));
		return e;
	}
	go(e) {
		e?.catch(Q);
	}
	shortcuts() {
		let e = F("div", "weft-keys"), t = F("dl");
		for (let [e, n] of Ki) t.appendChild(F("dt", void 0, e)), t.appendChild(F("dd", void 0, n));
		return e.appendChild(t), e;
	}
	header(e) {
		let t = F("div", "weft-head"), n = e.turns.some((e) => e.status === "running");
		t.appendChild(F("span", `weft-dot${e.live ? n ? " weft-run" : " weft-on" : ""}`, void 0, { title: e.live ? "live" : "history" }));
		let r = e.session?.agent ?? e.turns.at(0)?.agent ?? "", i = this.model?.publicId || e.session?.public_id || "", a = i ? `${r ? r + " · " : ""}${i}` : Ui;
		if (t.appendChild(F("span", "weft-title", a, { title: i ? a : `${a}: the newest runs are shown` })), !i) {
			let e = F("button", `weft-btn weft-howto-btn${this.howTo ? " weft-active" : ""}`, "how to scope", {
				type: "button",
				"aria-expanded": String(this.howTo),
				title: "the one-line ways to scope the panel to your conversation"
			});
			I(e, "click", () => {
				this.howTo = !this.howTo, this.render(this.last);
			}), t.append(" · ", e);
		}
		let o = this.switcher(e);
		o && t.appendChild(o);
		let s = this.model?.narrowing ?? {};
		s.session && t.appendChild(F("span", "weft-chip weft-scope-chip", `session ${s.session}`, { title: "the turn list is narrowed to this session" })), s.flow && t.appendChild(F("span", "weft-chip weft-scope-chip", `flow ${s.flow}`, { title: "the scope's flow — carried, filters nothing yet" })), t.appendChild(F("span", "weft-grow"));
		let c = e.turns.reduce((e, t) => e + t.usage.input_tokens, 0), l = e.turns.reduce((e, t) => e + t.usage.output_tokens, 0), u = `${e.turns.length}${e.turnsCapped ? "+" : ""} turns · ${V(c)}→${V(l)} tok`;
		if (t.appendChild(F("span", void 0, u, { title: u })), e.turns.length && e.selected) {
			let n = F("a", "weft-btn", "⤢", {
				href: Fi(this.base, e.selected, this.stepNow(e)),
				target: "_blank",
				rel: "noopener",
				title: "open in Studio (run, and the step you are reading)",
				"aria-label": "open in Studio"
			});
			n.style.textDecoration = "none", t.appendChild(n);
		}
		let d = this.lay.tab === "raw", f = F("button", `weft-btn${d ? " weft-active" : ""}`, "raw", {
			title: "the JSON, one keypress away (r)",
			"aria-pressed": String(d)
		});
		I(f, "click", () => this.toggleRaw()), t.appendChild(f);
		let p = this.lay.mode === "float" ? "float" : `dock ${this.lay.side}`, m = F("button", "weft-btn weft-layout", "⇆", {
			title: `layout: ${p} — next (Alt+Shift+W)`,
			"aria-label": `layout: ${p}`
		});
		I(m, "click", () => this.cycle()), t.appendChild(m), t.appendChild(this.themeButton());
		let h = F("button", "weft-btn", "–", {
			title: "collapse (Alt+W)",
			"aria-label": "collapse"
		});
		return I(h, "click", () => this.toggle()), t.appendChild(h), t;
	}
	switcher(e) {
		let t = this.conversations(), n = $(this.scopeNow()), r = t.some((e) => e.key === n);
		if (t.length < (r ? 2 : 1)) return null;
		let i = F("select", "weft-switch", void 0, {
			"aria-label": "conversation",
			"data-weft-k": "switch"
		}), a = (e, t, n, r) => {
			let a = F("option", void 0, e, {
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
		}), I(i, "change", (e, r) => {
			let i = r.value, a = i ? t.at(Number(i)) : void 0;
			a && a.key !== n && this.choose(a);
		}), i;
	}
	turnList(e) {
		let t = F("div", "weft-turns");
		this.note && t.appendChild(F("div", "weft-note weft-api-note", this.note, { role: "status" })), e.pinMissing && t.appendChild(F("div", "weft-note weft-pin-missing", `run ${e.pinMissing} not in this conversation`));
		let n = this.model?.narrowing.session;
		if (n && e.sessionUnrecorded && t.appendChild(F("div", "weft-note", `session ${n}: these runs carry no session id — not narrowed`)), !this.model?.publicId && e.devRefused ? t.appendChild(F("div", "weft-note weft-dev-poll", "streaming needs the server token · polling")) : !this.model?.publicId && e.devAgent && t.appendChild(F("div", "weft-note weft-dev-poll", `live: agent ${e.devAgent} · the other agents' runs every ${pr / 1e3} s`)), !e.turns.length && !e.experiments.size) {
			let e = n ? `no turns of session ${n} yet` : this.model?.publicId ? "no turns yet — run your app" : "no runs yet (dev)";
			return t.appendChild(F("div", "weft-splash", e)), t;
		}
		t.appendChild(this.turnFilter());
		let r = e.turns.filter((e) => this.match(e));
		this.filtering() && t.appendChild(F("div", "weft-tq-n", `${r.length} of ${e.turns.length} ${e.turnsCapped ? "loaded" : "turns"}${e.turnsCapped ? " · the filter applies to the loaded turns" : ""}`));
		let i = [...r, ...[...e.experiments.values()].flat()].map((e) => e.id), a = i.includes(this.rove) ? this.rove : i.includes(e.selected) ? e.selected : i[0], o = F("div", "weft-rows", void 0, {
			role: "list",
			"aria-label": "turns"
		}), s = (t) => this.turnRow(t, e.selected, a), c = new Set(e.turns.map((e) => e.id));
		for (let t of r) {
			o.appendChild(s(t));
			let n = e.experiments.get(t.id) ?? [];
			n.length && o.appendChild(F("div", "weft-expts", n.map(s), { "data-key": `x:${t.id}` }));
		}
		let l = [];
		for (let [t, n] of e.experiments) c.has(t) || l.push(...n.filter((e) => this.match(e)));
		if (l.length && o.appendChild(F("div", "weft-expts", l.map(s), { "data-key": "x:" })), t.appendChild(o), e.turnsCapped && this.model?.publicId) {
			let n = F("button", "weft-btn weft-older", e.loadingOlder ? "loading older turns…" : "older turns ↓", {
				type: "button",
				title: "load the next older page of turns",
				"data-key": `older:${e.olderAt}`
			});
			I(n, "click", () => this.go(this.model?.loadOlder())), t.appendChild(n);
		} else e.turnsCapped ? t.appendChild(F("div", "weft-note", "the newest 10 runs — older ones are in Studio (⤢)")) : e.paged && t.appendChild(F("div", "weft-tq-n weft-all", `all ${e.turns.length} turns loaded`));
		return t;
	}
	turnFilter() {
		let e = this.tq, t = F("div", "weft-tq", void 0, { "data-key": "tq" }), n = F("input", "weft-input weft-turn-q", void 0, {
			type: "search",
			"aria-label": "filter turns",
			placeholder: "/ filter turns"
		});
		n.value = e.text, I(n, "input", (t, n) => {
			e.text = n.value, this.render(this.last);
		});
		let r = F("select", "weft-input weft-tq-status", void 0, { "aria-label": "status" });
		for (let t of [
			"",
			"running",
			"succeeded",
			"failed",
			"parked"
		]) {
			let n = F("option", void 0, t || "any status", { value: t });
			n.selected = t === e.status, r.appendChild(n);
		}
		r.value = e.status, I(r, "change", (t, n) => {
			e.status = n.value, this.render(this.last);
		});
		let i = F("input", "weft-tq-err", void 0, { type: "checkbox" });
		return i.checked = e.err, I(i, "change", (t, n) => {
			e.err = n.checked, this.render(this.last);
		}), t.append(n, r, F("label", "weft-tool", [i, document.createTextNode("has error")])), t;
	}
	filtering() {
		return this.tq.text.trim() !== "" || !!this.tq.status || this.tq.err;
	}
	match(e) {
		let t = this.tq;
		if (t.status && Z(e) !== t.status || t.err && !e.err && e.status !== "failed") return !1;
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
		let r = Z(e), i = F("button", `weft-turn${e.id === t ? " weft-sel" : ""}`, void 0, {
			type: "button",
			tabindex: e.id === n ? "0" : "-1",
			...e.id === t ? { "aria-current": "true" } : {}
		}), a = F("div", "weft-row1", [
			F("span", `weft-chip weft-${r}`, r),
			F("span", "weft-id", e.id, { title: e.id }),
			...zt(e, (e) => {
				let t = this.rowOf(e);
				return t?.turn ? `t${t.turn}` : va(e);
			}),
			F("span", "weft-when", xn(e.last_seen || e.started))
		]), o = e.usage, s = F("div", "weft-row2", [
			F("span", void 0, e.model.name ? `${e.model.provider}/${e.model.name}` : ""),
			F("span", void 0, `${e.steps} steps`),
			F("span", void 0, `${V(o.input_tokens)}→${V(o.output_tokens)}`),
			F("span", void 0, Sn(e.started, e.finished) || "…")
		]);
		return i.append(a, s), e.err && i.appendChild(F("div", "weft-reason", e.err)), I(i, "click", () => this.go(this.model?.select(e.id, !0))), I(i, "focus", (t, n) => this.roveTo(n, e.id)), F("div", void 0, [i], {
			role: "listitem",
			"data-key": e.id
		});
	}
	roveTo(e, t) {
		this.rove = t;
		for (let t of Array.from(this.body.querySelectorAll(".weft-turn"))) t.tabIndex = t === e ? 0 : -1;
	}
	main(e) {
		let t = F("div", "weft-main");
		if (I(t, "toggle", (e) => {
			if (!(e.target instanceof HTMLElement)) return;
			let t = e.target, n = t.open, r = t.getAttribute("data-weft-open");
			r && this.openKeys.has(r) !== n && (n ? this.openKeys.add(r) : this.openKeys.delete(r), this.render(this.last));
			let i = t.getAttribute("data-weft-child");
			if (!i) return;
			t.querySelector("summary")?.setAttribute("aria-expanded", String(n));
			let a = this.model?.state.turn;
			a && (n ? (a.expanded.add(i), this.go(this.model?.expandChild(i))) : (a.expanded.delete(i), a.tried.delete(i), a.unreachable.delete(i)));
		}, !0), I(t, "click", (e) => {
			if (!(e.target instanceof Element)) return;
			let t = e.target.closest("[data-weft-step]");
			if (!t) return;
			let n = Number(t.getAttribute("data-weft-step"));
			Number.isFinite(n) && this.model?.selectStep(n);
		}), e.tooNew && e.meta) return t.appendChild(F("div", "weft-note weft-warn", [F("span", "weft-warn", "Studio is newer than this panel; update panel.js"), F("span", void 0, `studio_version ${e.meta.studio_version} · panel built for ${ir()}`)])), t;
		if (!e.turn) return t.appendChild(F("div", "weft-splash", "select a turn")), t;
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
		return a.appendChild(this.playgroundArea(e)), n !== "story" && a.setAttribute("hidden", ""), i(a, "story"), n !== "story" && i(F("div", void 0, [n === "raw" ? this.rawView(r) : n === "timeline" ? sa(r) : this.requestView(e, r)]), n), t;
	}
	tabs() {
		let e = F("div", "weft-tabs", void 0, {
			role: "tablist",
			"aria-label": "turn views",
			"data-key": "tabs"
		});
		for (let t of f) {
			let n = t === this.lay.tab, r = F("button", `weft-tab${n ? " weft-active" : ""}`, t[0].toUpperCase() + t.slice(1), {
				type: "button",
				role: "tab",
				id: `weft-tab-${t}`,
				"aria-selected": String(n),
				tabindex: n ? "0" : "-1",
				"data-key": `tab:${t}`,
				...n ? { "aria-controls": `weft-tp-${t}` } : {}
			});
			I(r, "click", () => this.setTab(t)), e.appendChild(r);
		}
		return I(e, "keydown", (e) => {
			let t = e.key, n = f.indexOf(this.lay.tab), r = f.length, i = t === "ArrowRight" ? n + 1 : t === "ArrowLeft" ? n - 1 : t === "Home" ? 0 : t === "End" ? r - 1 : null;
			if (i === null) return;
			e.preventDefault();
			let a = f[(i + r) % r];
			this.setTab(a), this.body.querySelector(`#weft-tab-${a}`)?.focus();
		}), e;
	}
	playgroundArea(e) {
		let t = F("div"), n = e.turn?.id ?? "";
		return e.result && e.result.sourceRunID === n && t.appendChild(this.experimentResult(e)), this.canAct(e) && e.drawer && e.drawer.runId === n && t.appendChild(this.drawer(e)), t;
	}
	canAct(e) {
		return Pi(e, "playground") && O(this.cfg.token) !== "read";
	}
	field(e, t) {
		return e.setAttribute("data-weft-k", t), e;
	}
	drawer(e) {
		let t = e.drawer;
		if (!t) return F("div");
		let n = e.runtimes.find((e) => e.id === t.runtimeId)?.agents.find((e) => e.name === t.agent), r = F("div", "weft-step weft-drawer"), i = F("div", "weft-step-h", [F("span", void 0, `Experiment · ${t.agent}${t.step > 0 ? ` · continue from step ${t.step}` : ""}`), F("span", "weft-grow")]), a = F("button", "weft-btn", "–", {
			title: "close the drawer",
			"aria-label": "close the drawer"
		});
		I(a, "click", () => this.model?.closeExperiment()), i.appendChild(a), r.appendChild(i);
		let o = F("div", "weft-step-b"), s = F("label", "weft-field", [F("span", void 0, "System prompt")]), c = this.field(F("textarea", "weft-input"), "prompt");
		c.rows = 3, c.value = t.instructions, I(c, "input", (e, t) => this.model?.setDraft({ instructions: t.value }, !0));
		let l = F("button", "weft-btn", "↺", {
			title: "reset to the registered prompt",
			"aria-label": "reset to the registered prompt"
		});
		if (I(l, "click", () => {
			this.model?.setDraft({ instructions: t.registeredInstructions });
		}), s.append(c, l), o.appendChild(s), n?.tools.length) {
			let e = F("div", "weft-field", [F("span", void 0, "Tools")]);
			for (let r of n.tools) {
				let n = F("input");
				n.type = "checkbox", n.checked = t.tools[r.name] ?? !0, I(n, "change", (e, n) => this.model?.setDraft({ tools: {
					...this.model.state.drawer?.tools ?? t.tools,
					[r.name]: n.checked
				} }));
				let i = F("label", "weft-tool", [n, F("span", void 0, r.name)]);
				(r.side_effects === "never" || !r.side_effects) && i.appendChild(F("span", "weft-badge weft-warn-badge", "⚠", { title: "side-effect tool (ReplayPolicy never): its calls substitute or park — never re-fire silently; only side effects: allow runs it for real, and only if the app opted it in" })), e.appendChild(i);
			}
			o.appendChild(e);
		}
		let u = F("div", "weft-fields"), d = F("select", "weft-input", void 0, { "aria-label": "model" }), f = e.turn?.doc?.model.name ?? "", p = F("option", void 0, `model: ${f || "—"}`);
		p.value = "", d.appendChild(p);
		for (let e of n?.models ?? []) {
			if (e === f) continue;
			let t = F("option", void 0, e);
			t.value = e, d.appendChild(t);
		}
		d.value = t.model, I(d, "change", (e, t) => this.model?.setDraft({ model: t.value })), u.appendChild(d);
		let m = F("select", "weft-input", void 0, { "aria-label": "thinking" }), h = F("option", void 0, "thinking: default");
		h.value = "", m.appendChild(h);
		for (let e of [
			"off",
			"low",
			"medium",
			"high"
		]) {
			let t = F("option", void 0, e);
			t.value = e, m.appendChild(t);
		}
		if (m.value = t.thinking, I(m, "change", (e, t) => this.model?.setDraft({ thinking: t.value })), u.appendChild(m), o.appendChild(u), t.step === 0) {
			let e = F("label", "weft-field", [F("span", void 0, "Input (replaces the user message)")]), n = this.field(F("textarea", "weft-input"), "input");
			n.rows = 2, n.value = t.input, I(n, "input", (e, t) => this.model?.setDraft({ input: t.value }, !0)), e.appendChild(n), o.appendChild(e);
		}
		let g = F("div", "weft-fields"), _ = F("select", "weft-input", void 0, { "aria-label": "side effects" });
		_.title = "How side-effect tools behave in the re-run. ReplaySafe tools always run; the others substitute, park, or — under allow, if the app opted them in — run for real.";
		let v = F("option", void 0, "side effects: substitute", { title: "a side-effect call the source recorded is answered from the record; any other call parks for you" });
		v.value = "", _.appendChild(v);
		let y = F("option", void 0, "park", { title: "every side-effect call parks for you; nothing is answered from the record" });
		y.value = "park", _.appendChild(y);
		let b = F("option", void 0, "allow — runs the tools this app opted in (AllowSideEffects) for real", { title: "refused unless every tool left on is opted in or ReplaySafe" });
		b.value = "allow", _.appendChild(b), _.value = t.sideEffects === "substitute" ? "" : t.sideEffects, I(_, "change", (e, t) => this.model?.setDraft({ sideEffects: t.value })), g.appendChild(_);
		let x = F("select", "weft-input", void 0, { "aria-label": "engine" }), S = F("option", void 0, "engine: live");
		S.value = "live", x.appendChild(S);
		let C = F("option", void 0, "scripted (zero tokens)");
		C.value = "scripted", x.appendChild(C), x.value = t.engine, I(x, "change", (e, t) => this.model?.setDraft({ engine: t.value })), g.appendChild(x);
		let w = F("select", "weft-input", void 0, { "aria-label": "thread" }), T = F("option", void 0, "thread: ephemeral");
		T.value = "ephemeral", w.appendChild(T);
		let ee = F("option", void 0, "fork (new session)");
		if (ee.value = "fork", w.appendChild(ee), w.value = t.thread, w.title = "fork continues the conversation in a new session (needs an input)", I(w, "change", (e, t) => this.model?.setDraft({ thread: t.value })), g.appendChild(w), o.appendChild(g), Pi(e, "breakpoints") && O(this.cfg.token) === "" && n?.tools.length) {
			let t = F("div", "weft-field");
			t.appendChild(F("span", void 0, "Break on (parks every run)", { title: "applies to runs this runtime starts — the app's own turns are viewer-only (PQ7)" }));
			for (let r of n.tools) {
				let n = F("input");
				n.type = "checkbox", n.checked = e.breakpoints.includes(r.name), I(n, "change", (t, n) => {
					let i = (this.model?.state.breakpoints ?? e.breakpoints).filter((e) => e !== r.name);
					n.checked && i.push(r.name), i.sort(), this.go(this.model?.setBreakpoints(i));
				}), t.appendChild(F("label", "weft-tool", [n, F("span", void 0, r.name)]));
			}
			o.appendChild(t);
		}
		let te = e.turn;
		if (t.step > 0 && te) {
			let e = () => this.model?.state.drawer ?? t, n = F("div", "weft-field");
			n.appendChild(F("span", void 0, `Transcript edits (steps 0..${t.step - 1} are kept)`));
			for (let [r, i] of te.folded.steps.entries()) {
				if (r >= t.step) break;
				for (let t of i.toolCalls) {
					if (!t.result) continue;
					let i = F("label", "weft-edit", void 0, { "data-key": `${r}:${t.callId}` });
					i.appendChild(F("span", void 0, `step ${r} · ${t.name} →`));
					let a = this.field(F("input", "weft-input"), `edit:${r}:${t.callId}`);
					a.placeholder = String(t.result.content).slice(0, 60);
					let o = () => e().edits.find((e) => e.step === r && e.callID === t.callId);
					a.value = o()?.toolResult ?? "", I(a, "input", (n, i) => {
						let a = i.value, s = o(), c = [...e().edits], l = s ? c.indexOf(s) : -1;
						a === "" ? l >= 0 && c.splice(l, 1) : l >= 0 ? c[l] = {
							...s,
							toolResult: a,
							step: r,
							callID: t.callId
						} : c.push({
							step: r,
							callID: t.callId,
							toolResult: a
						}), this.model?.setDraft({ edits: c }, !0);
					}), i.appendChild(a), n.appendChild(i);
				}
				if (i.text && !i.toolCalls.length) {
					let t = F("label", "weft-edit", void 0, { "data-key": `${r}` });
					t.appendChild(F("span", void 0, `step ${r} · reply`));
					let i = this.field(F("textarea", "weft-input"), `edit:${r}`);
					i.rows = 2;
					let a = () => e().edits.find((e) => e.step === r && !e.callID);
					i.value = a()?.content ?? "", I(i, "input", (t, n) => {
						let i = n.value, o = a(), s = [...e().edits], c = o ? s.indexOf(o) : -1;
						i === "" ? c >= 0 && s.splice(c, 1) : c >= 0 ? s[c] = {
							...o,
							content: i,
							step: r
						} : s.push({
							step: r,
							content: i
						}), this.model?.setDraft({ edits: s }, !0);
					}), t.appendChild(i), n.appendChild(t);
				}
			}
			n.childElementCount > 1 && o.appendChild(n);
		}
		let E = F("button", "weft-run-btn", "Run experiment ▶", { title: "POST /api/playground/runs — the runtime in your app executes it" });
		return I(E, "click", () => this.go(this.model?.runExperiment())), o.appendChild(E), r.appendChild(o), r;
	}
	experimentResult(e) {
		let t = e.result;
		if (!t) return F("div");
		let n = F("div", "weft-step weft-xres"), r = t.row?.usage, i = [
			t.state,
			r ? `${V(r.input_tokens)}→${V(r.output_tokens)} tok` : "",
			t.row ? Sn(t.row.started, t.row.finished) : ""
		].filter(Boolean).join(" · "), a = F("div", "weft-step-h", [
			F("span", void 0, `Result · ${t.label}`),
			F("span", void 0, i),
			F("span", "weft-grow")
		]), o = F("button", "weft-btn", "keep as prompt ⤴", { title: "copy the edited prompt (weft/prompt versions are post-v1, PQ2)" });
		I(o, "click", () => {
			let e = this.model?.state.drawer?.instructions ?? "";
			try {
				navigator.clipboard?.writeText(e).catch(Q);
			} catch {}
		}), a.appendChild(o);
		let s = F("a", "weft-btn", "save as fixture", {
			href: X(this.base, ji(t.runID ? { run: t.runID } : {})),
			target: "_blank",
			rel: "noopener",
			title: "hand off to Studio: the run's records as wefttest replay fixtures (D4)"
		});
		s.style.textDecoration = "none", a.appendChild(s);
		let c = F("a", "weft-btn", "compare in Studio", { title: "open the Studio playground with this run, step and the current overrides carried over" }), l = (n) => {
			let r = this.model?.state.drawer ?? null, i = r && r.runId === t.sourceRunID ? r : null, a = i && i.step > 0 ? i.step : e.turn ? oa(e.turn.folded, e.selectedStep) : -1;
			n.setAttribute("href", ya(this.base, i, a));
		};
		l(c), c.setAttribute("target", "_blank"), c.setAttribute("rel", "noopener");
		for (let e of [
			"pointerdown",
			"focus",
			"click",
			"contextmenu"
		]) I(c, e, (e, t) => l(t));
		c.style.textDecoration = "none", a.appendChild(c);
		let u = F("button", "weft-btn", "discard", { title: "clear the result pane" });
		I(u, "click", () => this.model?.discardResult()), a.appendChild(u), n.appendChild(a);
		let d = F("div", "weft-step-b");
		t.error && d.appendChild(F("div", "weft-note weft-warn", t.error));
		let f = t.row?.status ?? (t.ready ? "succeeded" : "running");
		for (let e of t.folded.steps) {
			e.text && d.appendChild(F("div", void 0, e.text));
			for (let n of e.toolCalls) d.appendChild(ma(n, e.index, f, void 0, void 0, t.runID ? {
				endpoint: this.base,
				runId: t.runID
			} : void 0));
		}
		!t.folded.steps.length && !t.error && t.state === "queued" ? d.appendChild(F("div", "weft-note", "queued — waiting for the runtime to ack…")) : !t.folded.steps.length && !t.error && t.state === "accepted" && !t.runID && d.appendChild(F("div", "weft-note", "accepted — the fork's turn is running in its new session…"));
		let p = [{
			id: "",
			label: ba(t.label)
		}, ...(e.experiments.get(t.sourceRunID) ?? []).filter((e) => e.id !== t.runID).map((e) => ({
			id: e.id,
			label: va(e.id)
		}))];
		if (p.length > 1) {
			let e = F("select", "weft-input", void 0, { "aria-label": "compare with" });
			for (let t of p) {
				let n = F("option", void 0, `compare vs ${t.label || "source"}`);
				n.value = t.id, e.appendChild(n);
			}
			e.value = t.compareWith, I(e, "change", (e, t) => this.go(this.model?.setCompare(t.value))), d.appendChild(e);
		}
		let m = t.ready ? t.words ?? $n(t.folded) : null, h = t.compareWith ? this.model?.compareWords.get(t.compareWith) : t.source, g = t.compareWith ? va(t.compareWith) : ba(t.label);
		if (m && h && m.text && h.text) {
			let e = F("div", "weft-diff");
			this.diffInto(e, `diff vs ${g}:`, h.text, m.text), h.calls.join("\n") !== m.calls.join("\n") && this.diffInto(e, "tool calls:", h.calls.join("\n"), m.calls.join("\n")), d.appendChild(e);
		}
		if (t.ready && t.folded.pending.length && t.runID && this.canAct(e) && d.appendChild(this.decisions(t.folded.pending, t.decided)), Pi(e, "steer") && this.canAct(e) && t.state === "accepted" && t.runID) {
			let e = F("div", "weft-step");
			e.appendChild(F("div", "weft-step-h", [F("span", void 0, "steer this run")]));
			let t = F("div", "weft-step-b"), n = this.field(F("input", "weft-input"), "steer");
			n.placeholder = "a message delivered mid-flight", n.value = this.scratch.get("steer") ?? "", I(n, "input", (e, t) => this.scratch.set("steer", t.value));
			let r = F("button", "weft-btn", "steer", { title: "POST /api/runs/{id}/steer (ADR 0019)" });
			I(r, "click", (e, t) => {
				let n = t.parentElement?.querySelector("input");
				n?.value && (this.go(this.model?.steer(n.value)), this.scratch.delete("steer"), n.value = "");
			}), t.append(n, r), e.appendChild(t), d.appendChild(e);
		}
		return n.appendChild(d), n;
	}
	diffInto(e, t, n, r) {
		if ((n.split("\n").length + 1) * (r.split("\n").length + 1) > 25e4) {
			e.appendChild(F("div", "weft-diff-h", `${t}  too large for the panel — compare in Studio`));
			return;
		}
		let i = _n(n, r);
		e.appendChild(F("div", "weft-diff-h", `${t}  ${vn(i)}`));
		for (let t of i) t.kind !== "same" && e.appendChild(F("div", `weft-diff-row weft-diff-${t.kind}`, `${t.kind === "add" ? "+" : "−"} ${t.text}`));
	}
	decisions(e, t) {
		let n = F("div", "weft-step");
		n.appendChild(F("div", "weft-step-h", [F("span", void 0, "awaiting decision")]));
		let r = F("div", "weft-step-b"), i = e.filter((e) => !t[e.id]).length;
		i < e.length && r.appendChild(F("div", "weft-note", `waiting for ${i} more decision${i === 1 ? "" : "s"} — the run resumes once every parked call is decided`));
		let a = {
			approve: "continue",
			deny: "skip",
			resolve: "resolve"
		};
		for (let n of e) {
			let e = F("div", "weft-call", void 0, { "data-key": n.id }), i = F("div", "weft-call-h", [F("span", "weft-name", n.name), F("span", "weft-args", n.args === void 0 ? "(…)" : Et(n.args))]);
			t[n.id] && i.appendChild(F("span", "weft-badge weft-info", `decided: ${a[t[n.id]] ?? t[n.id]}`)), e.appendChild(i);
			let o = F("div", "weft-res"), s = `resolve:${n.id}`, c = this.field(F("input", "weft-input weft-resolve"), s);
			c.placeholder = "the result to resolve with", c.value = this.scratch.get(s) ?? "", I(c, "input", (e, t) => this.scratch.set(s, t.value));
			let l = (e, t, n) => {
				let r = F("button", "weft-btn", e, { title: t });
				return I(r, "click", (e, t) => n(t)), r;
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
		if (!t) return F("div");
		let n = F("div");
		n.appendChild(this.notes(t));
		let r = this.turnLinks(t);
		r && n.appendChild(r);
		let i = Zn(t.transcript);
		i && Bi(this.prompts, t.id, i, 500), i && n.appendChild(F("div", "weft-note", i));
		let a = Tn(t.doc);
		for (let e of a.filter(H)) n.appendChild(da(e, a, t.transcript, {
			keys: this.openKeys,
			scope: t.id
		}));
		let o = this.rowOf(t.id);
		return n.appendChild(la(t.folded, o?.status ?? t.doc?.status ?? "running", t, e.selectedStep, {
			keys: this.openKeys,
			scope: t.id
		}, {
			endpoint: this.base,
			runId: t.id
		})), t.folded.pending.length && n.appendChild(this.approvals(t.folded.pending, !!o?.playground)), this.canAct(e) && n.appendChild(this.actions(e)), n;
	}
	turnLinks(e) {
		let t = this.rowOf(e.id), n = t?.session_id || e.doc?.session_id || "", r = t?.trace_id || e.doc?.trace_id || "";
		if (!n && !r) return null;
		let i = F("div", "weft-row2"), a = (e, t, n, r, i) => {
			let a = F("a", "weft-chip", e, {
				href: t,
				target: "_blank",
				rel: "noopener",
				title: n,
				[r]: i
			});
			return a.style.textDecoration = "none", a;
		};
		return n && i.appendChild(a(`session ${n}`, X(this.base, ki(n)), "the session in Studio", "data-weft-session-link", n)), r && i.appendChild(a(`trace ${r.slice(0, 8)}`, X(this.base, Ai(r)), `the OTel trace ${r} in Studio`, "data-weft-trace-link", r)), i;
	}
	actions(e) {
		let t = e.turn;
		if (!t) return F("div");
		let n = F("div", "weft-actions"), r = F("button", "weft-btn", "✎ Experiment", { title: "open the experiment drawer, pre-filled from the registered config" });
		I(r, "click", () => this.go(this.model?.openExperiment(t.id, 0))), n.appendChild(r);
		let i = F("button", "weft-btn", "↻ Re-run", { title: "re-run the whole turn with the drawer's current edits" });
		I(i, "click", () => this.go(this.model?.rerun(t.id))), n.appendChild(i);
		let a = oa(t.folded, e.selectedStep);
		if (a > 0) {
			let e = F("button", "weft-btn", `⎇ Continue from step ${a}`, { title: "keep the transcript through the previous step (edits apply) and run this step fresh" });
			I(e, "click", () => this.go(this.model?.openExperiment(t.id, a))), n.appendChild(e);
		}
		return n;
	}
	notes(e) {
		let t = F("div");
		t.setAttribute("data-weft-turn-holes", "");
		let n = ia(e, this.rowOf(e.id));
		for (let e of n) t.appendChild(jt(e));
		return e.capped && t.appendChild(F("div", "weft-note weft-warn", "a long run: the first 10000 events are shown — the whole story is in Studio (⤢)")), t;
	}
	approvals(e, t) {
		let n = F("div", "weft-step");
		n.appendChild(F("div", "weft-step-h", [F("span", void 0, "awaiting decision (read-only)")]));
		let r = F("div", "weft-step-b");
		for (let n of e) {
			let e = F("div", "weft-call", void 0, { "data-key": n.id });
			e.appendChild(F("div", "weft-call-h", [
				F("span", "weft-name", n.name),
				F("span", "weft-args", n.args === void 0 ? "(…)" : Et(n.args)),
				F("span", "weft-badge weft-info", "parked")
			])), e.appendChild(F("div", "weft-res", t ? "an experiment's run — its decision controls are in the result pane of the turn that ran it" : "the app's own turns are viewer-only (PQ7) — decide from your app")), r.appendChild(e);
		}
		return n.appendChild(r), n;
	}
	footer(e) {
		let t = [F("span", void 0, "prompts, args and results from your app, via your Studio")], n = Lt(e.turn, e.meta, 1e4);
		n && t.push(F("span", "weft-cap", [document.createTextNode(` · ${n.text}`), F("span", "weft-sr", ` — ${n.title}`)], {
			title: n.title,
			"data-weft-cap": n.hole ?? ""
		}));
		let r = this.detectWord();
		return t.push(F("span", "weft-detect", ` · detect: ${r}${this.rung?.chained ? " (chained)" : ""}${this.notRestored && !this.rung ? " (fetch not restored: patched after the panel)" : ""}`, { title: r.startsWith("url") ? "scope from the page URL's weft_scope" : r.includes("headers") || r === "markers" ? "reading Weft-Scope on same-origin fetches / data-weft-scope markers" : "scope from data-scope / window.__WEFT__" })), this.globalNote && t.push(F("span", "weft-global", ` · global: ${this.globalNote}`)), F("div", "weft-footer", t, { "data-key": "foot" });
	}
	rawView(e) {
		this.treeFor !== e.id && (this.tree = {
			...jr(),
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
		}), Hr(this.rawMemo.doc, this.tree, e.id.replace(/[\\/]/g, "_"), {
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
		return Ei({
			t,
			step: this.stepNow(e),
			running: r === "running",
			manifest: () => this.readManifest(n, t.doc?.manifest_hash),
			notServed: e.meta?.capabilities_off?.requests,
			keys: this.openKeys,
			tree: (e) => {
				let t = this.rqTrees.get(e);
				return t || this.rqTrees.set(e, t = jr()), t;
			},
			cx: {
				redraw: n,
				root: () => this.shadow
			},
			select: (e) => this.model?.selectStep(e),
			pick: this.rqPick,
			redraw: n,
			compaction: (e) => da(e, Tn(t.doc), t.transcript, i),
			child: (n) => {
				let r = n.childRunId ?? "", a = t.children.get(r), o = F("div", "weft-call", [F("div", "weft-call-h", [F("span", "weft-name", n.name, { title: r }), F("span", "weft-args", "subagent · step 0")])], {
					"data-key": `sub:${n.callId}`,
					"data-weft-rq-child": r
				});
				return a ? o.appendChild(a.requests ? pa(0, a.requests, a.status, i) : F("div", "weft-req", R("not_recorded", {
					cause: "not_served",
					reason: e.meta?.capabilities_off?.requests
				}))) : t.unreachable.has(r) ? o.appendChild(F("div", "weft-reason", [F("span", void 0, "child's history unreachable · "), F("a", void 0, "open in Studio (⤢)", {
					href: Fi(this.base, r),
					target: "_blank",
					rel: "noopener",
					"data-weft-handoff": r
				})])) : t.tried.has(r) ? o.appendChild(F("div", "weft-reason", "reading…")) : o.appendChild(I(F("button", "weft-btn", "read its turn", { type: "button" }), "click", () => this.go(this.model?.expandChild(r)))), o;
			}
		});
	}
	readManifest(e, t) {
		if (O(this.cfg.token) === "read") return null;
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
};
function ia(e, t) {
	return A(mt(e.doc, e.folded), Ue({
		status: t?.status ?? e.doc?.status,
		stop_reason: t?.stop_reason ?? e.doc?.stop_reason,
		gaps: e.gaps
	}));
}
function aa(e) {
	if (e.selectedStep != null) return e.selectedStep;
	let t = e.turn;
	if (t && t.id === e.selected) return ([...e.turns, ...[...e.experiments.values()].flat()].find((e) => e.id === t.id)?.status ?? t.doc?.status) === "running" ? t.folded.steps.at(-1)?.index : void 0;
}
function oa(e, t) {
	return t == null ? -1 : e.steps.findIndex((e) => e.index === t);
}
function sa(e) {
	let t = e.spans ?? [], n = Dt(t), r = Ot(t), i = "time", a = 0;
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
	let o = F("div", "weft-timeline", void 0, { "data-axis": i }), s = t.length - n.placed;
	if (s > 0 && o.appendChild(F("div", "weft-note weft-warn", `${s} span${s === 1 ? "" : "s"} not placed: unreadable times, or an end before the start`)), !r.length) return o.appendChild(F("div", "weft-note", "nothing to place yet: no spans and no steps")), o;
	let c = F("div", "weft-axis");
	for (let e of [
		0,
		.25,
		.5,
		.75,
		1
	]) {
		let t = Math.round(a * e), n = F("span", "weft-tick", i === "time" ? `${t} ms` : `seq ${t}`);
		n.style.left = `${e * 100}%`, c.appendChild(n);
	}
	return o.appendChild(F("div", "weft-wf-row", [
		F("span", "weft-wf-name", i === "time" ? "time (ms)" : "seq"),
		c,
		F("span", "weft-wf-ms")
	])), o.appendChild(ca(r)), o;
}
function ca(e) {
	let t = F("div", "weft-wf");
	for (let n of e) {
		let e = F("div", "weft-wf-row");
		e.appendChild(F("span", "weft-wf-name", n.name, { title: n.name }));
		let r = F("span", "weft-wf-track"), i = F("span", "weft-wf-bar");
		i.style.left = `${(n.left * 100).toFixed(2)}%`, i.style.width = `${(n.width * 100).toFixed(2)}%`, r.appendChild(i), e.appendChild(r), e.appendChild(F("span", "weft-wf-ms", n.label ?? `${n.ms}ms`)), t.appendChild(e);
	}
	return t;
}
function la(e, t, n, r, i, a) {
	let o = F("div");
	e.model?.name && o.appendChild(F("div", "weft-reason", `${e.model.provider}/${e.model.name}`));
	let s = t === "running" && !a?.child ? e.steps.at(-1) : void 0;
	for (let c of e.steps) o.appendChild(ua(c, t, n, r, i, a, c === s));
	return o;
}
function ua(e, t, n, r, i, a, o = !1) {
	let s = F("div", "weft-step", void 0, { "data-key": `s${e.index}` });
	s.setAttribute("data-weft-step", String(e.index)), r === e.index && (s.style.outline = "1px solid var(--weft-accent)");
	let c = F("div", "weft-step-h", [F("span", void 0, `step ${e.index}`), F("span", "weft-grow")]), l = a?.child ? a.child.requests : n?.requests, u = l?.steps.get(e.index)?.rows ?? [], d = !l?.error && (!l?.truncated || e.index < fa(l));
	e.finish && (c.appendChild(F("span", void 0, e.finish.reason)), c.appendChild(F("span", void 0, Sa(e.finish.usage))));
	let f = d ? Nn(Mn(u, !!e.finish || e.toolCalls.length > 0, t === "running")) : null;
	if (f && c.appendChild(F("span", "weft-badge weft-info", f, { "data-weft-attempts": "" })), e.finish) {
		let t = Fn(e.finish.latencyMs, e.finish.ttftMs, "ttft");
		t && c.appendChild(F("span", void 0, t, { "data-weft-timing": "" }));
	}
	let p = pt(e, a?.child ? a.child.doc?.holes : n?.doc?.holes), m = d ? In(e.finish, u.length) : null, h = At(m ? A(p, [m]) : p);
	h && c.appendChild(h), s.appendChild(c);
	let g = F("div", "weft-step-b"), _ = Tn(a?.child ? a.child.doc : n?.doc);
	for (let t of _) !H(t) && t.step === e.index && g.appendChild(da(t, _, a?.child ? a.child.transcript : n?.transcript, i));
	if (l && g.appendChild(pa(e.index, l, t, i)), e.reasoning) {
		let t = F("details", "weft-collapsible");
		if (i) {
			let n = `${i.scope}\u0000reasoning\u0000${e.index}`;
			t.setAttribute("data-weft-open", n), i.keys.has(n) && t.setAttribute("open", "");
		}
		t.appendChild(F("summary", void 0, "reasoning")), t.appendChild(F("div", void 0, e.reasoning)), g.appendChild(t);
	}
	o ? g.appendChild(F("div", "weft-stream", e.text, {
		"aria-live": "polite",
		"aria-busy": "true"
	})) : e.text && g.appendChild(F("div", void 0, e.text)), e.steer && g.appendChild(F("div", "weft-note", `steered: ${e.steer.text}`));
	for (let r of e.toolCalls) g.appendChild(ma(r, e.index, t, n, i, a));
	return s.appendChild(g), s;
}
function da(e, t, n, r) {
	let i = H(e), a = F("div", "weft-note");
	a.setAttribute("data-weft-compaction", i ? "session" : String(e.step ?? ""));
	let o = F("div", "weft-call-h", [F("span", "weft-name", i ? Dn : "compaction"), F("span", "weft-args", En(e))]), s = At([{ hole: "compacted" }]);
	s && o.appendChild(s), a.appendChild(o);
	let c = F("details", "weft-collapsible");
	if (r) {
		let t = `${r.scope}\u0000compaction\u0000${i ? `session\u0000${e.hash}` : `view\u0000${e.index ?? ""}`}`;
		c.setAttribute("data-weft-open", t), r.keys.has(t) && c.setAttribute("open", "");
	}
	if (c.appendChild(F("summary", void 0, "show original")), i) c.appendChild(F("div", "weft-res", On(e)));
	else {
		let r = kn(e, n, t);
		if ("loading" in r) c.appendChild(F("div", "weft-res", "loading the transcript…"));
		else if ("gap" in r) {
			let e = At([{
				hole: "gap",
				reason: r.gap
			}]);
			e && c.appendChild(e), c.appendChild(F("div", "weft-reason", r.gap));
		} else if (!r.messages.length) c.appendChild(F("div", "weft-res", `nothing replaced: inserted at message ${r.from}`));
		else for (let [e, t] of r.messages.entries()) c.appendChild(F("div", "weft-res", An(t), { "data-weft-original": String(r.from + e) }));
		c.appendChild(F("div", "weft-reason", jn(e)));
	}
	return a.appendChild(c), a;
}
function fa(e) {
	let t = -1;
	for (let n of e.steps.keys()) n > t && (t = n);
	return t;
}
function pa(e, t, n, r) {
	let i = F("div", "weft-req");
	if (i.setAttribute("data-weft-request", String(e)), t.badge) return i.append(...R(t.badge, {
		reason: t.reason,
		fix: t.fix
	})), i;
	if (t.error) return i.appendChild(F("span", "weft-badge weft-err", `request could not be read: ${t.error}`)), i;
	let a = t.steps.get(e), o = a?.rows[a.rows.length - 1];
	if (!a || !o) return i.appendChild(n !== "running" && t.truncated ? Nt(10 * en) : n === "running" ? F("span", "weft-res", `request: ${tt}`) : F("span", void 0, R("gap", { reason: Xe }))), i;
	let s = F("div", "weft-call-h", [F("span", "weft-name", "request"), F("span", "weft-args", a.rows.map((e) => `attempt ${e.attempt}`).join(" · "))]);
	a.promptChanged && s.appendChild(F("span", "weft-badge weft-info", "prompt changed at this step")), a.catalogChanged && s.appendChild(F("span", "weft-badge weft-info", "catalog changed at this step")), o.content && o.content !== "stripped" && s.appendChild(L(o.content)), i.appendChild(s), o.content === "stripped" && i.append(...R("stripped"));
	let c = o.prompt;
	if (c && !Fe(c)) {
		let t = F("details", "weft-collapsible");
		if (r) {
			let n = `${r.scope}\u0000request\u0000${e}`;
			t.setAttribute("data-weft-open", n), r.keys.has(n) && t.setAttribute("open", "");
		}
		let n = c.text;
		t.appendChild(F("summary", void 0, `system prompt · ${n.length} chars`)), t.appendChild(F("div", "weft-res", n)), i.appendChild(t);
	} else if (o.system_hash) {
		let e = F("div", "weft-res", `system prompt ${M(o.system_hash)}`);
		c && e.append(" · ", L(c.badge)), i.appendChild(e);
	}
	let l = o.body.tools.names;
	return i.appendChild(F("div", "weft-res", `tools: ${l.length ? l.join(", ") : "none"}`)), i.appendChild(F("div", "weft-res", `params: ${Je(o)}`)), i;
}
function ma(e, t, n, r, i, a) {
	let o = F("div", "weft-call", void 0, { "data-key": e.callId }), s = ht(e, n), c = a?.runId, l = F("div", "weft-call-h", [a?.endpoint && c ? F("a", "weft-name", e.name, {
		href: X(a.endpoint, Oi(c, {
			step: t,
			call: e.callId,
			resumed: e.resumed
		})),
		target: "_blank",
		rel: "noopener",
		title: `open this call in Studio (step ${t})`,
		"data-weft-call-link": e.callId
	}) : F("span", "weft-name", e.name), F("span", "weft-args", xa(e))]);
	if (o.appendChild(l), e.childRunId && (r || a?.child) && (l.appendChild(a?.endpoint ? F("a", "weft-badge weft-info", "subagent", {
		href: Fi(a.endpoint, e.childRunId),
		target: "_blank",
		rel: "noopener",
		title: e.childRunId,
		"data-weft-subagent-link": e.childRunId
	}) : F("span", "weft-badge weft-info", "subagent", { title: e.childRunId })), a?.child ? a.endpoint && l.appendChild(_a(a.endpoint, e.childRunId)) : r && o.appendChild(ga(e.childRunId, r, i, a?.endpoint))), e.result) {
		let t = ha(r, e);
		t && l.appendChild(F("span", "weft-badge weft-info", t));
		let n = yt(String(e.result.content));
		n && l.appendChild(Pt(n)), e.result.isError && l.appendChild(F("span", "weft-badge weft-err", "error"));
		let i = At(e.holes ?? []);
		i && l.appendChild(i), o.appendChild(F("div", "weft-res", e.result.content));
	} else s === "running" ? o.appendChild(F("div", "weft-res", "running…")) : o.appendChild(F("div", "weft-res weft-warn", "never completed"));
	return o;
}
function ha(e, t) {
	if (!e?.spans) return "";
	let n = e.spans.filter((e) => e.name === "execute_tool"), r = n.find((e) => e.attrs["gen_ai.tool.call.id"] === t.callId) ?? n.find((e) => e.attrs["gen_ai.tool.call.id"] === void 0 && e.attrs["gen_ai.tool.name"] === t.name);
	if (!r) return "";
	let i = Date.parse(r.end) - Date.parse(r.start);
	return !Number.isFinite(i) || i < 0 ? "" : `${Math.round(i)}ms`;
}
function ga(e, t, n, r) {
	let i = t.children.get(e), a = t.doc?.children.find((t) => t.id === e), o = F("details", "weft-collapsible");
	o.setAttribute("data-weft-child", e), t.expanded.has(e) && o.setAttribute("open", "");
	let s = F("summary", void 0, a ? `subagent ${a.agent || va(e)} · ${a.status} · ${We(a.status) ? Sa(a.usage) : a.status === "running" ? Ge : "—"}` : `subagent ${va(e)}`);
	s.setAttribute("aria-expanded", String(t.expanded.has(e)));
	let c = a && At(i?.doc?.holes ?? He(a));
	if (c && s.appendChild(c), o.appendChild(s), r && o.appendChild(_a(r, e)), !i) o.appendChild(F("div", void 0, "loading the subagent's turn…"));
	else {
		let t = a?.status ?? "succeeded";
		o.appendChild(la(i.folded, t, void 0, void 0, n && {
			keys: n.keys,
			scope: e
		}, {
			endpoint: r,
			child: i,
			runId: e
		})), i.capped && o.appendChild(F("div", "weft-note weft-warn", "a long run: its first events are shown"));
	}
	return o;
}
function _a(e, t) {
	return F("a", "weft-btn", "open in Studio ⤢", {
		href: Fi(e, t),
		target: "_blank",
		rel: "noopener",
		title: `open the child run ${t} in Studio`,
		"data-weft-handoff": t
	});
}
function va(e) {
	let t = e.split("/");
	return t[t.length - 1] || e;
}
function ya(e, t, n) {
	let r = {};
	if (t) {
		t.runId && (r.run = t.runId), n != null && n > 0 && (r.step = n), t.instructions && t.instructions !== t.registeredInstructions && (r.instructions = t.instructions);
		let e = Object.entries(t.tools).filter(([, e]) => e).map(([e]) => e);
		e.length && e.length < Object.keys(t.tools).length && (r.tools = e.join(",")), t.model && (r.model = t.model), t.thinking && (r.thinking = t.thinking), t.input && t.step === 0 && (r.input = t.input), t.engine === "scripted" && (r.engine = t.engine), t.sideEffects && t.sideEffects !== "substitute" && (r.side_effects = t.sideEffects), t.thread === "fork" && (r.thread = t.thread), t.agent && (r.agent = t.agent), t.runtimeId && (r.runtime = t.runtimeId);
	}
	return X(e, ji(r));
}
function ba(e) {
	return e.split("·")[0] || e;
}
function xa(e) {
	if (e.args !== void 0) try {
		return `(${JSON.stringify(e.args)})`;
	} catch {
		return "(?)";
	}
	return e.streamedArgs ? `(${e.streamedArgs}…)` : "(…)";
}
function Sa(e) {
	let t = [`${V(e.input_tokens)}→${V(e.output_tokens)} tok`];
	return e.cached_input_tokens && t.push(`${V(e.cached_input_tokens)} cached`), e.reasoning_tokens && t.push(`${V(e.reasoning_tokens)} reasoning`), e.cache_write_tokens && t.push(`${V(e.cache_write_tokens)} cache-write`), t.join(" · ");
}
//#endregion
//#region src/panel/main.ts
function Ca() {
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
function wa() {
	let e = document.createElement("weft-devtools");
	e.autoMounted = !0, document.body?.appendChild(e);
}
function Ta() {
	if (customElements.get("weft-devtools") || customElements.define("weft-devtools", ra), document.readyState === "loading") {
		document.addEventListener("DOMContentLoaded", () => {
			try {
				Ea();
			} catch {}
		}, { once: !0 });
		return;
	}
	Ea();
}
function Ea() {
	Ca();
	let e = D();
	document.querySelector("weft-devtools") || (e.auto || Me()) && wa();
}
try {
	Ta();
} catch {}
//#endregion
