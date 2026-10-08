//#region src/lib/scope.ts
var e = [
	"session",
	"flow",
	"run"
], t = (e) => {
	try {
		return encodeURIComponent(e);
	} catch {
		return e.replace(/[%;=]/g, (e) => `%${e.charCodeAt(0).toString(16).toUpperCase()}`);
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
//#region src/panel/config.ts
function d(e) {
	let t = new Set(String(e ?? "").toLowerCase().split(",").map((e) => e.trim()).filter(Boolean));
	if (t.has("off")) return "off";
	let n = t.delete("headers"), r = t.delete("markers");
	return t.size ? "" : n && r ? "headers,markers" : n ? "headers" : r ? "markers" : "";
}
var f = [
	"bottom-right",
	"bottom-left",
	"right-dock"
], p = [
	"endpoint",
	"scope",
	"public-id",
	"token",
	"detect",
	"position",
	"open",
	"auto"
].map((e) => `data-${e}`), m = (() => {
	try {
		let e = document.currentScript;
		return e && e.tagName === "SCRIPT" ? e : null;
	} catch {
		return null;
	}
})();
function h() {
	if (m?.isConnected) return m;
	try {
		return document.querySelector("script[data-weft]") || document.querySelector(p.map((e) => `script[${e}]`).join(","));
	} catch {
		return null;
	}
}
function g(e, t) {
	try {
		let n = new URL(e, t);
		return n.protocol !== "http:" && n.protocol !== "https:" ? "" : (n.pathname.endsWith("/") || (n.pathname += "/"), n.toString());
	} catch {
		return "";
	}
}
function _(e) {
	let t = e?.getAttribute("src");
	if (!t) return "";
	try {
		return g("./", new URL(t, document.baseURI).toString());
	} catch {
		return "";
	}
}
function ee(e) {
	return (t) => {
		if (!e) return null;
		let n = {
			endpoint: e.endpoint,
			scope: v(e.scope),
			"public-id": e.publicId,
			token: e.token,
			detect: e.detect,
			position: e.position,
			open: e.open,
			auto: e.auto
		}[t];
		return n === void 0 ? null : String(n);
	};
}
function v(e) {
	if (typeof e == "string") return e;
	if (e && typeof e == "object" && typeof e.publicId == "string") try {
		return r(e);
	} catch {
		return;
	}
}
function te() {
	return (e) => {
		try {
			return document.querySelector(`meta[name="weft:${e}"]`)?.getAttribute("content") ?? null;
		} catch {
			return null;
		}
	};
}
var y = (e) => (t) => e?.getAttribute(`data-${t}`) ?? null;
function b(e) {
	let t = h(), n = [
		ee(e?.options),
		y(e),
		te(),
		y(t)
	], r = (e) => {
		for (let t of n) {
			let n = t(e);
			if (n !== null && (e !== "endpoint" || n.trim() !== "")) return n;
		}
		return null;
	}, a = _(t), o = r("endpoint"), s = o === null ? a || g("./", document.baseURI) : g(o, document.baseURI), c = r("position"), l = r("open"), u = null;
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
	u ??= S();
	let p = r("detect");
	return {
		endpoint: s,
		endpointExplicit: o !== null,
		configURL: o === null && a ? a + "panel-config.json" : "",
		publicId: u.publicId,
		scope: u,
		scopeExplicit: !!(u.publicId || u.session || u.flow || u.run),
		urlScope: w(),
		token: r("token") ?? "",
		detect: d(p),
		position: f.includes(c) ? c : "bottom-right",
		open: l === "true" || l === "",
		auto: r("auto") !== "false"
	};
}
async function ne(e, t) {
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
		let i = g(r.endpoint, e);
		return !i || new URL(i).origin !== new URL(e).origin ? "" : i;
	} catch {
		return "";
	}
}
function x() {
	try {
		let e = window.__WEFT__?.publicId;
		return e == null ? "" : String(e);
	} catch {
		return "";
	}
}
function S() {
	try {
		let e = window.__WEFT__?.scope;
		if (typeof e == "string") return i(e);
		let t = v(e);
		if (t !== void 0) return i(t);
	} catch {}
	return { publicId: x() };
}
var C = { href: () => location.href };
function re(e) {
	for (let t of e.replace(/^[?#]/, "").split("&")) {
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
function w(e = C.href()) {
	try {
		let t = new URL(e);
		for (let e of [t.search, t.hash]) {
			let t = re(e);
			if (t === null) continue;
			let n = i(t);
			if (n.publicId) return n;
		}
	} catch {}
	return null;
}
function ie(e, t = C.href()) {
	return e.detect === "off" ? !1 : e.detect.includes("headers") ? !0 : c(t) && c(e.endpoint) && !e.token.startsWith("weft_pt.");
}
function ae(e, t = C.href()) {
	return e.detect === "off" ? !1 : e.detect.includes("markers") ? !0 : c(t) || T(e.token) !== "read";
}
function T(e) {
	if (!e.startsWith("weft_pt.")) return "";
	try {
		let t = e.slice(8).split(".")[0].replace(/-/g, "+").replace(/_/g, "/");
		return JSON.parse(atob(t + "=".repeat((4 - t.length % 4) % 4))).scope === "playground" ? "playground" : "read";
	} catch {
		return "read";
	}
}
function oe() {
	try {
		if (new URLSearchParams(location.search).get("weft") === "debug" || localStorage.getItem("weft_debug") === "1") return !0;
	} catch {}
	return !1;
}
//#endregion
//#region src/lib/api.ts
function se(e) {
	let t = e?.batches;
	return Array.isArray(t) ? { batches: t.map((e) => {
		let t = ce(e.messages), n = !Array.isArray(e.messages) || t.length !== e.messages.length;
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
function ce(e) {
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
function le(e) {
	return typeof e.badge == "string";
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
}, ue = Object.keys(E);
function de(e) {
	return e !== void 0 && Object.prototype.hasOwnProperty.call(E, e);
}
function fe(e) {
	return e < 1024 ? `${e} B` : e < 1048576 ? `${(e / 1024).toFixed(1)} KiB` : `${(e / 1048576).toFixed(1)} MiB`;
}
function pe(e) {
	if (typeof e != "object" || !e) return [];
	let t = e, n = [], r = t["weft.content"];
	(r === "stripped" || r === "none") && n.push({ hole: "stripped" }), r === "redacted" && n.push({ hole: "redacted" });
	let i = Number(t["weft.content.truncated_bytes"]);
	return Number.isFinite(i) && i > 0 && n.push({
		hole: "truncated",
		bytes: i
	}), n;
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
		let t = ue.indexOf(e);
		return t < 0 ? ue.length : t;
	};
	return [...t.values()].sort((e, t) => n(e.hole) - n(t.hole));
}
function O(e) {
	let t = de(e.hole) ? E[e.hole] : void 0;
	return {
		label: e.hole === "truncated" && e.bytes ? `shortened by the recorder: ${fe(e.bytes)} cut` : t?.label ?? e.hole,
		reason: e.reason || t?.reason || e.hole,
		fix: e.fix || t?.fix,
		tone: t?.tone ?? "loss"
	};
}
function me(e) {
	let t = Array.isArray(e.holes) ? [...e.holes] : [];
	return e.requests_badge === "not_recorded" && t.push({ hole: "not_recorded" }), e.status === "interrupted" && t.push({ hole: "interrupted" }), D(t);
}
function he(e) {
	let t = [];
	e.status === "interrupted" && t.push({ hole: "interrupted" });
	let n = Array.isArray(e.gaps) ? e.gaps : [];
	return n.length && e.status && e.status !== "running" && t.push({
		hole: "gap",
		reason: `${n.length} ${n.length === 1 ? "event" : "events"} missing (${n.length === 1 ? "position" : "positions"} ${n.slice(0, 8).join(", ")}${n.length > 8 ? ", …" : ""}): a destination dropped a batch`
	}), e.stop_reason === "max_tokens" && t.push({ hole: "max_tokens" }), t;
}
function ge(e) {
	return e === "succeeded" || e === "failed";
}
var _e = "usage at finish";
//#endregion
//#region src/lib/requests.ts
function ve(e) {
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
function ye(e) {
	return e.length > 12 ? e.slice(0, 12) : e;
}
function be(e) {
	let t = e.body.params, n = (e) => e == null ? "adapter default" : JSON.stringify(e);
	return [
		["temperature", n(t.temperature)],
		["top_p", n(t.top_p)],
		["max_tokens", n(t.max_tokens)],
		["stop", t.stop === void 0 && e.content === "stripped" ? "not recorded (stripped)" : n(t.stop)],
		["seed", n(t.seed)]
	];
}
function xe(e) {
	return be(e).map(([e, t]) => `${e} ${t}`).join(" · ");
}
var Se = "request not recorded by weft v0.9.0 or earlier", Ce = "not stored yet — the run is still running";
//#endregion
//#region src/lib/live.ts
function we(e) {
	let [[t, n]] = Object.entries(e);
	return `${encodeURIComponent(t)}=${encodeURIComponent(n)}`;
}
var Te = 6e4, k = class extends Error {
	status;
	constructor(e) {
		super(`live grant refused: ${e}`), this.status = e;
	}
};
function Ee(e) {
	return (e ?? ["event", "run"]).join(",");
}
async function De(e, t, n, r, i) {
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
			kinds: Ee(r)
		}),
		signal: i
	});
	if (!o.ok) throw new k(o.status);
	let s = await o.json();
	if (typeof s?.sig != "string" || !s.sig) throw new k(o.status);
	return {
		sig: s.sig,
		exp: Oe(s.exp, o.headers.get("Date"))
	};
}
function Oe(e, t, n = Date.now()) {
	let r = typeof e == "string" ? Date.parse(e) : NaN, i = t ? Date.parse(t) : NaN;
	return !Number.isFinite(r) || !Number.isFinite(i) ? n + Te : n + Math.min(Te, Math.max(0, r - i - 1e3));
}
function ke(e, t, n, r) {
	let i = new URL(e);
	return i.search = we(t), i.searchParams.set("kinds", Ee(n)), i.searchParams.set("sig", r.sig), i.toString();
}
function Ae(e, t = Date.now()) {
	return t >= e.exp;
}
//#endregion
//#region src/panel/client.ts
function A(e, t) {
	return new URL(t, new URL("api/", e.base)).toString();
}
var j = class extends Error {
	status;
	code;
	body;
	constructor(e, t, n, r) {
		super(n), this.status = e, this.code = t, this.body = r;
	}
};
async function M(e, t, n) {
	let r = { Accept: "application/json" };
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(A(e, t), {
		headers: r,
		signal: n
	});
	if (!i.ok) {
		let e = "network", t = `${i.status} ${i.statusText}`, n;
		try {
			let r = await i.json();
			n = r, r.error && (e = r.error.code ?? e, t = r.error.message ?? t);
		} catch {}
		throw new j(i.status, e, t, n);
	}
	return await i.json();
}
function je(e, t) {
	return M(e, "meta", t);
}
function Me(e, t, n) {
	let r = new URLSearchParams(t).toString();
	return M(e, `runs${r ? `?${r}` : ""}`, n);
}
function Ne(e, t, n) {
	return M(e, `runs/${encodeURIComponent(t)}`, n);
}
function Pe(e, t, n, r) {
	return M(e, `runs/${encodeURIComponent(t)}/events?after=${n}&limit=500`, r);
}
function N(e, t, n) {
	return M(e, `runs/${encodeURIComponent(t)}/transcript`, n).then(se);
}
var Fe = 1e3;
async function Ie(e, t, n) {
	let r = [], i = 0;
	for (let a = 0; a < 10; a++) {
		let a;
		try {
			a = await M(e, `runs/${encodeURIComponent(t)}/requests?limit=${Fe}${i ? `&from=${i}` : ""}`, n);
		} catch (e) {
			let t = e instanceof j && e.status === 403 ? e.body : null;
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
function Le(e, t, n) {
	return M(e, `runs/${encodeURIComponent(t)}/spans`, n);
}
function Re(e, t, n) {
	let r = new URLSearchParams(t).toString();
	return M(e, `sessions${r ? `?${r}` : ""}`, n);
}
function ze(e, t) {
	return M(e, "runtimes", t);
}
function Be(e, t) {
	return P(e, "playground/runs", t);
}
function Ve(e, t) {
	return M(e, `playground/commands/${encodeURIComponent(t)}`);
}
function He(e, t, n) {
	return P(e, `runs/${encodeURIComponent(t)}/approvals`, n);
}
function Ue(e, t, n) {
	return Ge(e, `runtimes/${encodeURIComponent(t)}/breakpoints`, { tools: n });
}
function We(e, t, n) {
	return P(e, `runs/${encodeURIComponent(t)}/steer`, { message: n });
}
async function Ge(e, t, n) {
	let r = {
		Accept: "application/json",
		"Content-Type": "application/json"
	};
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(A(e, t), {
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
		throw new j(i.status, e, t);
	}
	return await i.json();
}
async function P(e, t, n) {
	let r = {
		Accept: "application/json",
		"Content-Type": "application/json"
	};
	e.token && (r.Authorization = `Bearer ${e.token}`);
	let i = await fetch(A(e, t), {
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
		throw new j(i.status, e, t);
	}
	return await i.json();
}
var Ke = 1e4;
function F(e, t) {
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
		De(A(e, "live-grant"), e.token, t.selector, t.kinds).then((e) => {
			!n && r === o && f(e);
		}, (e) => {
			if (!(n || r !== o)) {
				if (t.onRefused && e instanceof k && e.status === 403) {
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
			f = new EventSource(ke(A(e, "live"), t.selector, t.kinds, o));
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
				if (r = !0, f.readyState === EventSource.CLOSED || Ae(o)) {
					if (f.close(), s >= 1) {
						u("closed");
						return;
					}
					s++, d();
				}
				i ||= setTimeout(() => {
					i = null, !(n || a?.readyState === EventSource.OPEN) && u("closed");
				}, Ke);
			}
		};
	};
	return d(), { close: l };
}
//#endregion
//#region src/lib/diff.ts
function qe(e, t) {
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
function Je(e) {
	let t = e.filter((e) => e.kind === "add").length, n = e.filter((e) => e.kind === "del").length;
	return !t && !n ? "identical" : `+${t} −${n}`;
}
//#endregion
//#region src/lib/events.ts
function I(e) {
	return typeof e == "string" ? e : "";
}
function L(e, t) {
	return typeof e == "number" && Number.isInteger(e) && e >= 0 ? e : t;
}
function Ye(e) {
	return typeof e == "number" && Number.isFinite(e) && e > 0;
}
function Xe(e) {
	let t = typeof e == "object" ? e : null;
	return {
		...t,
		input_tokens: t?.input_tokens ?? 0,
		output_tokens: t?.output_tokens ?? 0
	};
}
function R() {
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
			let f = pe(d);
			f.length && (t[i] = f, e.holes = D(e.holes, f));
			let p, m;
			if (typeof l == "object" && l) {
				switch (l.type) {
					case "run_start":
						e.runId = I(l.id), e.agent = typeof l.agent == "string" ? l.agent : void 0, e.model = l.model, e.startPos = i;
						break;
					case "step_start":
						a = !0, p = o(L(l.index, c()));
						break;
					case "text_delta":
						p = o(c()), p.text += I(l.text);
						break;
					case "reasoning_delta":
						p = o(c()), p.reasoning += I(l.text);
						break;
					case "tool_args_delta":
						n.set(l.name, (n.get(l.name) ?? "") + I(l.args));
						break;
					case "tool_start":
						p = o(c()), m = {
							callId: I(l.call_id),
							name: I(l.name),
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
								content: I(l.content),
								isError: !!l.is_error
							}, t.state = "done", t.finishPos = i;
							for (let n of e.steps) n.toolCalls.includes(t) && (p = n, i > n.to && (n.to = i));
						}
						break;
					}
					case "step_finish":
						p = o(L(l.index, c())), p.finish = {
							reason: I(l.reason),
							raw: l.raw,
							usage: Xe(l.usage)
						}, Ye(l.latency_ms) && (p.finish.latencyMs = l.latency_ms), Ye(l.ttft_ms) && (p.finish.ttftMs = l.ttft_ms);
						break;
					case "steered": {
						let e = o(L(l.step, c()));
						p = e;
						let t = (Array.isArray(l.messages) ? l.messages : []).map((e) => $e(e)).filter(Boolean).join("\n");
						e.steer = {
							text: (e.steer?.text ? e.steer.text + "\n" : "") + t,
							pos: i
						};
						break;
					}
					case "run_finish": e.finished = !0, e.usage = Xe(l.usage), e.pending = Array.isArray(l.pending) ? l.pending : [], e.finishPos = i;
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
function Ze(e, t) {
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
function Qe(e) {
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
function $e(e, t = "") {
	let n = e?.content;
	return Array.isArray(n) ? n.filter((e) => e?.type === "text" && typeof e.text == "string").map((e) => e.text).join(t) : "";
}
function et(e) {
	let t = Array.isArray(e) ? e : [], n = tt(t);
	return t.map((e, t) => {
		let r = Qe(e), i = e?.step, a = e?.input, o = e?.badge;
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
function tt(e) {
	let t = -1;
	return e.map((e, n) => {
		let r = Qe(e), i = e?.input, a = typeof i == "boolean" ? i : n === 0 && (r.length !== 1 || r[0].role !== "assistant");
		return !a && r.some((e) => e.role === "assistant") && t++, {
			step: Math.max(t, 0),
			input: a
		};
	});
}
function nt(e) {
	let t = [], n = [];
	for (let r of et(e)) (r.input ? t : n).push(...r.messages);
	return {
		input: t,
		produced: n
	};
}
function rt(e) {
	let { input: t } = nt(e);
	for (let e = t.length - 1; e >= 0; e--) {
		if (t[e].role !== "user") continue;
		let n = $e(t[e], "\n");
		if (n) return n;
	}
	return null;
}
function it(e, t, n) {
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
	for (let e of et(t)) {
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
			let i = $e(t);
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
function at(e, t, n) {
	n?.status === "running" && (n = void 0);
	let r = [...e.holes ?? []];
	return e.derived && r.push({
		hole: "derived",
		reason: "this step's words come from a transcript batch whose step was inferred, not stored"
	}), e.finish?.reason === "max_tokens" && r.push({ hole: "max_tokens" }), D(Array.isArray(n?.holes) ? n.holes : [], r, (t ?? []).filter((e) => e.hole === "not_recorded" || e.hole === "stripped"));
}
function ot(e, t) {
	return D(Array.isArray(e?.holes) ? e.holes : [], t.holes);
}
function st(e, t) {
	return e.state === "done" ? "done" : t === "running" ? "running" : "never";
}
var ct = /…\[truncated (\d+) bytes\]/u, lt = /^tool call (.+) was not executed: the response hit the output token limit$/;
function ut(e) {
	let t = ct.exec(e);
	if (t) return {
		kind: "bytes",
		bytes: Number(t[1])
	};
	let n = lt.exec(e);
	return n ? {
		kind: "call",
		tool: n[1]
	} : null;
}
//#endregion
//#region src/lib/format.ts
function dt(e, t = Date.now()) {
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
function ft(e, t) {
	let n = Date.parse(e), r = t ? Date.parse(t) : NaN;
	return Number.isNaN(n) || Number.isNaN(r) || r < n ? "—" : pt(r - n);
}
function pt(e) {
	if (!Number.isFinite(e)) return "—";
	if (e < 1e3) return `${Math.round(e)}ms`;
	if (e < 59950) return `${(e / 1e3).toFixed(1)}s`;
	let t = Math.round(e / 1e3), n = Math.floor(t / 60);
	return n < 60 ? `${n}m${String(t % 60).padStart(2, "0")}s` : `${Math.floor(n / 60)}h${String(n % 60).padStart(2, "0")}m`;
}
function z(e) {
	if (!Number.isFinite(e)) return "—";
	let t = Math.abs(e);
	return t >= 999950 ? `${(e / 1e6).toFixed(1)}M` : t >= 1e3 ? `${(e / 1e3).toFixed(1)}k` : String(e);
}
//#endregion
//#region src/lib/compaction.ts
var B = (e, t) => `${e} ${t}${e === 1 ? "" : "s"}`;
function V(e) {
	return e.scope === "session";
}
function mt(e) {
	return Array.isArray(e?.compactions) ? e.compactions : [];
}
function ht(e) {
	if (V(e)) {
		let t = e.tokens_before && e.tokens_after ? ` · ${z(e.tokens_before)} → ${z(e.tokens_after)} tokens` : "";
		return `${B(e.replaced, "message")} compacted into ${e.entries}${t}`;
	}
	return e.replaced === 0 ? `${B(e.entries, "message")} inserted by PrepareStep` : `${B(e.replaced, "message")} rewritten into ${e.entries} by PrepareStep`;
}
var gt = "session compaction · after this run";
function _t(e) {
	return `thread compacted the session context this run belongs to: ${e.replaced} of its messages were replaced by ${e.entries}; the next run starts on the compacted context (its input record). The marker carries counts and a hash, never messages. (For entries appended by hand that no run produced, thread files the marker under the run that follows, which starts on the compacted context.)`;
}
function vt(e, t, n) {
	if (!t) return { loading: !0 };
	let r = e.index ?? -1, i = e.from_seq ?? -1, a = e.to_seq ?? -1;
	if (r < 0 || i < 0 || a < i) return { gap: "the view names no usable range: its replaced messages cannot be placed" };
	let o = new Set(n.filter((e) => !V(e) && e.index != null).map((e) => e.index)), s = (Array.isArray(t.batches) ? t.batches : []).filter((e) => e !== null && typeof e.index == "number" && e.index < r).sort((e, t) => e.index - t.index), c = new Set(s.map((e) => e.index)), l = [];
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
function yt(e, t = 160) {
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
function bt(e, t) {
	let n = t == null ? "" : ` — the step's request carried ${B(t, "message")} in all`;
	return `in their place, ${B(e.entries, "message")}${n}; the view's body stays in its record (export the run: compactions[].messages)`;
}
//#endregion
//#region src/lib/attempts.ts
function xt(e, t, n = !1) {
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
function St(e) {
	if (!e) return null;
	if (e.n === 0) return e.total > 0 ? `${e.total} ${e.total === 1 ? "attempt" : "attempts"} · none answered` : null;
	if (e.n <= 1) return null;
	let t = `attempt ${e.n} of ${Math.max(e.total, e.n)}`;
	return e.requested && e.answered && (t += e.answered === e.requested ? " · retry" : ` · fallback to ${e.answered}`), t;
}
function Ct(e) {
	if (e < 1e3) return `${Math.round(e)} ms`;
	if (e < 59950) return `${(e / 1e3).toFixed(1)} s`;
	let t = Math.round(e / 1e3);
	return `${Math.floor(t / 60)}m${String(t % 60).padStart(2, "0")}s`;
}
function wt(e, t, n = "first token") {
	let r = [];
	return e && e > 0 && r.push(Ct(e)), t && t > 0 && r.push(`${n} ${Ct(t)}`), r.length ? r.join(" · ") : null;
}
function Tt(e, t) {
	return e === void 0 || e.latencyMs || t !== 0 ? null : {
		hole: "not_recorded",
		reason: "this step's step_finish has no timing and its request record no attempt rows: it was recorded by a weft before attempt reporting (A4)"
	};
}
//#endregion
//#region src/panel/markers.ts
var Et = "data-weft-scope", Dt = "weft-devtools";
function Ot(e) {
	try {
		return !(e instanceof Element) || e.closest(Dt) ? null : e.closest(`[${Et}]`);
	} catch {
		return null;
	}
}
function kt(e) {
	if (e.type === "attributes") return !(e.target instanceof Element && e.target.closest(Dt));
	for (let t of [...Array.from(e.addedNodes), ...Array.from(e.removedNodes)]) if (t instanceof Element && (t.hasAttribute("data-weft-scope") || t.querySelector("[data-weft-scope]"))) return !0;
	return !1;
}
function At(e = document) {
	let t = [];
	for (let n of Array.from(e.querySelectorAll(`[${Et}]`))) {
		if (n.closest(Dt)) continue;
		let e = i(n.getAttribute("data-weft-scope") ?? "");
		e.publicId && t.push({
			scope: e,
			element: n
		});
	}
	return t;
}
function jt(e) {
	let t = e.root ?? document, n = null, r = null, i = 0, a = !1, o = () => {
		if (n = null, i = 0, t.hidden) {
			a = !0;
			return;
		}
		a = !1;
		try {
			e.onScopes(At(t));
		} catch {}
	}, s = () => {
		a && !t.hidden && o();
	}, c = (t) => {
		let n = Ot(t.target);
		if (n) try {
			e.onFocus?.(n);
		} catch {}
	};
	try {
		r = new MutationObserver((t) => {
			if (!t.some(kt)) return;
			let r = Date.now();
			i ||= r, n && clearTimeout(n), n = setTimeout(o, Math.max(0, Math.min(e.debounceMs ?? 100, i + 500 - r)));
		}), r.observe(t.documentElement, {
			subtree: !0,
			childList: !0,
			attributes: !0,
			attributeFilter: [Et]
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
//#region src/panel/render.ts
function H(e, t, n, r) {
	let i = document.createElement(e);
	if (t && (i.className = t), typeof n == "string") i.textContent = n;
	else if (Array.isArray(n)) for (let e of n) i.appendChild(e);
	if (r) for (let [e, t] of Object.entries(r)) i.setAttribute(e, t);
	return i;
}
function Mt(e, t) {
	return JSON.stringify(e, null, t);
}
function Nt(e) {
	try {
		return Mt(e, 2) ?? String(e);
	} catch {
		return String(e);
	}
}
function Pt(e) {
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
var Ft = "ui-monospace, SFMono-Regular, Menlo, Consolas, \"Liberation Mono\", monospace", It = `
:host { all: initial; box-sizing: border-box; }
*, *::before, *::after { box-sizing: inherit; }
.weft-root {
  --w-bg: #101418; --w-bg2: #161b21; --w-bg3: #1d242c;
  --w-fg: #d7dee6; --w-dim: #8b98a5; --w-faint: #5c6873;
  --w-line: #2a333d; --w-accent: #4cc38a; --w-warn: #e5b567;
  --w-err: #e06c75; --w-info: #6cb6ff;
  font: 12px/1.45 ${Ft};
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
  font: 13px ${Ft};
  display: flex; align-items: center; justify-content: center;
}
.weft-fab-bottom-left { right: auto; left: 16px; }
.weft-fab:hover { background: var(--w-bg2); }
.weft-fab::before { content: "\\25C8"; color: var(--w-accent); margin-right: 4px; }

/* C2: a mount the host made, with no Studio answering — one quiet
   line in the page's flow, where the host put the element. */
.weft-unreachable { display: inline; font: 12px/1.45 ${Ft}; color: #8b98a5; }
.weft-unreachable-at { color: inherit; }
.weft-retry { background: none; border: none; padding: 0; color: #6cb6ff; cursor: pointer;
  text-decoration: underline; font: inherit; }

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
.weft-fab-running { border-color: var(--w-warn); width: auto; min-width: 40px; border-radius: 20px; padding: 0 10px; }
.weft-fab-count { color: var(--w-warn); font-variant-numeric: tabular-nums; }
.weft-fab-pulse { animation: weft-pulse 1.2s infinite; }
.weft-howto { display: flex; flex-direction: column; gap: 2px; padding: 6px 10px; border-bottom: 1px solid var(--w-line); color: var(--w-dim); }
@media (prefers-reduced-motion: reduce) { .weft-dot.weft-run, .weft-fab-pulse { animation: none; } }
.weft-head .weft-title { overflow: hidden; text-overflow: ellipsis; }
.weft-head .weft-grow { flex: 1; }
.weft-switch { max-width: 40%; background: var(--w-bg); color: var(--w-dim); border: 1px solid var(--w-line);
  border-radius: 5px; font: inherit; font-size: 11px; }
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

.weft-req { margin-bottom: 6px; }
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
.weft-holes { display: inline-flex; flex-wrap: wrap; gap: 4px; }
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
//#region src/panel/playground.ts
function Lt(e) {
	return e.content.filter((e) => e?.type === "text").map((e) => e.text).join("");
}
function Rt(e, t) {
	let n = "";
	if (t !== void 0) try {
		n = Mt(t) ?? "";
	} catch {
		n = "?";
	}
	return `${e}(${n})`;
}
function zt(e) {
	return e ? rt(e.batches) ?? "" : "";
}
function Bt(e) {
	if (!e || !e.batches.length) return null;
	let { input: t, produced: n } = nt(e.batches), r = t.length;
	for (let e = t.length - 1; e >= 0 && t[e].role !== "user"; e--) r = e;
	let i = [...t.slice(r), ...n].filter((e) => e.role === "assistant"), a = [];
	for (let e of i) for (let t of e.content) t?.type === "tool_call" && a.push(Rt(t.name, t.args));
	return {
		text: i.map(Lt).filter(Boolean).join("\n"),
		calls: a
	};
}
function U(e) {
	return {
		text: e.steps.map((e) => e.text).filter(Boolean).join("\n"),
		calls: e.steps.flatMap((e) => e.toolCalls).map((e) => Rt(e.name, e.args))
	};
}
function Vt(e, t) {
	let n = /-t(\d+)$/.exec(e);
	return `${n ? `t${n[1]}` : e.slice(-8)}·x${t + 1}`;
}
function Ht(e) {
	let t = Object.keys(e.tools);
	return t.length && !t.some((t) => e.tools[t]) ? "at least one tool must stay on — the command cannot express an empty tool set (it would run with every tool)" : null;
}
function Ut(e, t) {
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
function Wt(e, t) {
	let n = e.filter((e) => e.agents.some((e) => e.name === t));
	if (!n.length) return e[0] ?? null;
	let r = (e) => Date.parse(e.last_seen) || 0;
	return n.reduce((e, t) => r(t) > r(e) ? t : e);
}
//#endregion
//#region src/panel/version.ts
function Gt() {
	return "v0.11.0";
}
function Kt(e) {
	if (typeof e != "string") return null;
	let t = /^v?(\d+(?:\.\d+)*)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]*)?$/.exec(e.trim());
	return t ? {
		nums: t[1].split(".").map(Number),
		pre: t.at(2) ?? ""
	} : null;
}
function qt(e, t) {
	let n = Kt(e), r = Kt(t);
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
function Jt(e) {
	return qt(e, Gt()) > 0;
}
var Yt = 700, Xt = 8, Zt = 1e4, Qt = 1e3;
function W() {
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
		devAgent: "",
		devRefused: !1,
		raw: !1,
		pinMissing: "",
		sessionUnrecorded: !1
	};
}
function $t(e) {
	let t = R();
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
function en(e) {
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
function tn(e) {
	return !!e?.holes?.some((e) => e.hole === "stripped");
}
function nn(e, t, n, r) {
	if (t && typeof t == "object" && typeof t.type == "string") try {
		e.push(t, n, r);
	} catch {}
}
function G(e, t, n) {
	for (let r of n) r && typeof r.pos == "number" && !t.has(r.pos) && (t.add(r.pos), nn(e, r.event, r.pos, r.attrs));
}
function rn(e, t) {
	let n = R(), r = /* @__PURE__ */ new Set();
	G(n, r, t), e.feed = n, e.seen = r, e.events = t, e.pos = r.size ? Math.max(...r) : -1, e.stale = !0;
}
function an(e, t, n = !1) {
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
	return nn(e.feed, t.event, r, t.attrs), e.stale = !0, "folded";
}
function K(e) {
	let t = e.result();
	return Array.isArray(t.pending) || (t.pending = []), t;
}
function on(e, t, n = !1) {
	if (!t) return e;
	try {
		return it(e, t.batches, { replace: n });
	} catch {
		return e;
	}
}
var q = () => {}, sn = class {
	state = W();
	notify;
	ep;
	scopeSub;
	runSub;
	expSub;
	devSub;
	disposed = !1;
	loadSeq = 0;
	timers = /* @__PURE__ */ new Set();
	retries = {
		scope: 0,
		run: 0,
		exp: 0,
		dev: 0
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
	constructor(e, t, n) {
		this.ep = e, this.notify = n;
		let r = typeof t == "string" ? { publicId: t } : t;
		this.publicId = r.publicId, this.narrowing = un(r);
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
			let t = await je(this.ep);
			if (!t || typeof t != "object" || typeof t.studio_version != "string") throw Error("not a Studio");
			e = {
				...t,
				capabilities: Array.isArray(t.capabilities) ? t.capabilities : []
			};
		} catch {
			return this.state.gone = !0, !1;
		}
		return !this.disposed && (this.state.meta = e, this.state.tooNew = Jt(e.studio_version), this.emit(), this.state.tooNew || await this.scope(), !0);
	}
	async rescope(e, t = {}) {
		let n = typeof e == "string" ? { publicId: e } : e, i = un(n), a = r({
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
		};
		let t = this.state;
		t.live = !1, t.devAgent = "", t.devRefused = !1, t.session = null, t.turns = [], t.turnsCapped = !1, t.experiments = /* @__PURE__ */ new Map(), t.selected = "", t.selectedStep = null, t.turn = null, t.drawer = null, t.result = null, t.pinMissing = "", t.sessionUnrecorded = !1, this.pinApplied = "", this.pinChecks = 0, this.pinTimer = null, this.userSelected = !1, this.compareWords.clear(), this.emit(), this.subscribe(e), this.armDev(), await this.refresh();
	}
	subscribe(e) {
		if (this.scopeSub?.close(), this.scopeSub = void 0, !this.publicId) return;
		let t = async () => {
			e !== this.loadSeq || this.disposed || (this.subscribe(e), await this.refresh());
		};
		this.scopeSub = F(this.ep, {
			selector: { public_id: this.publicId },
			kinds: ["run"],
			onOpen: (e) => {
				this.retries.scope = 0, this.state.live || (this.state.live = !0, this.emit()), e && this.refresh().catch(q);
			},
			onRun: (e) => this.onRunFrame(e),
			onOverflow: (e) => {
				this.scopeSub = void 0, this.state.live = !1, this.emit(), e !== "expired" && (e === "overflow" ? t().catch(q) : this.retry("scope", t));
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
		this.cancel(this.devTimer), this.devTimer = null, !(!this.watching || this.publicId || this.disposed || !this.state.meta || this.state.tooNew) && (this.devSub && this.state.turns.every((e) => e.agent === this.state.devAgent) || (this.devTimer = this.after(Zt, () => {
			if (this.devTimer = null, typeof document < "u" && document.visibilityState === "hidden") {
				this.armDev();
				return;
			}
			this.refresh().catch(q).then(() => this.armDev());
		})));
	}
	armStale() {
		if (this.cancel(this.staleTimer), this.staleTimer = null, this.disposed || ![...this.state.turns, ...[...this.state.experiments.values()].flat()].some((e) => e.status === "running")) return;
		let e = this.state.meta?.interrupted_after_ms, t = (typeof e == "number" && e > 0 ? Math.min(e, 6e5) : 3e4) + 1e3;
		this.staleTimer = this.after(t, () => {
			this.staleTimer = null, this.refresh().catch(q);
		});
	}
	subscribeDev(e) {
		let t = this.state;
		if (this.publicId || this.devSub || t.devRefused || this.disposed || !t.meta || t.tooNew) return;
		if (T(this.ep.token) !== "") {
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
		this.devSub = F(this.ep, {
			selector: { agent: n },
			kinds: ["run"],
			onOpen: (e) => {
				this.retries.dev = 0, e && this.refresh().catch(q);
			},
			onRun: (e) => this.onRunFrame(e),
			onRefused: () => {
				t.devRefused = !0, r();
			},
			onOverflow: (e) => {
				r(), e !== "expired" && (e === "overflow" ? i().catch(q) : this.retry("dev", i));
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
				let t = await Re(this.ep, { public_id: this.publicId }).catch(() => null);
				if (e !== this.loadSeq || this.disposed) return;
				if (t && Array.isArray(t.sessions)) {
					let e = this.narrowing.session;
					this.state.session = (e ? t.sessions.find((t) => t.id === e) : t.sessions[0]) ?? null;
				}
			}
			let n = await Me(this.ep, this.publicId ? {
				public_id: this.publicId,
				limit: "50"
			} : { limit: "10" });
			if (e !== this.loadSeq || this.disposed) return;
			let { turns: r, experiments: i } = en((Array.isArray(n.runs) ? n.runs : []).filter((e) => !!e && typeof e.id == "string")), a = this.narrowing.session;
			this.state.sessionUnrecorded = !!a && r.length > 0 && !r.some((e) => e.session_id), this.state.turns = r.filter((e) => this.inSession(e)), this.state.experiments = i, this.state.turnsCapped = n.next_before != null;
			for (let e of t) this.upsertRun(e);
		} catch {
			return;
		} finally {
			this.collectors.delete(t);
		}
		this.armStale(), this.publicId || (this.subscribeDev(e), this.armDev());
		let n = this.state.turn;
		if (n && !n.done && !n.loading) {
			let e = this.rowOf(n.id)?.status;
			e && e !== "running" && this.settleTurn(n).catch(q);
		}
		let r = this.takePin();
		if (r) await this.select(r);
		else if (this.state.selected) this.emit();
		else {
			let e = this.state.turns.find((e) => e.status === "running") ?? this.state.turns.at(0);
			e ? await this.select(e.id) : this.emit();
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
		return this.pinChecks++, t ? (this.state.pinMissing = "", this.pinApplied === e || this.userSelected ? "" : (this.pinApplied = e, e)) : (this.state.pinMissing = this.pinChecks >= 2 ? e : "", this.pinChecks === 1 && !this.pinTimer && (this.pinTimer = this.after(Qt, () => {
			this.pinTimer = null, this.refresh().catch(q);
		})), "");
	}
	onRunFrame(e) {
		let t = e.run;
		if (this.publicId && t.public_id && t.public_id !== this.publicId || t.parent_run_id || !this.inSession(t)) return;
		for (let e of this.collectors) e.push(t);
		this.upsertRun(t), this.publicId || (this.state.turns = this.state.turns.slice(0, 10)), this.armStale();
		let n = this.takePin();
		if (n) {
			this.select(n).catch(q);
			return;
		}
		if (!this.state.selected && this.state.turns.length) {
			let e = this.state.turns.find((e) => e.status === "running");
			this.select((e ?? this.state.turns[0]).id).catch(q);
			return;
		}
		let r = this.state.turn;
		r && r.id === t.id && (t.status === "running" ? r.done && !this.runSub && !r.loading && (r.done = !1, this.resumeTurn(r).catch(q)) : this.settleTurn(r).catch(q)), this.emit();
	}
	upsertRun(e) {
		let t = this.state.turns.filter((t) => t.id !== e.id), n = /* @__PURE__ */ new Map();
		for (let [t, r] of this.state.experiments) {
			let i = r.filter((t) => t.id !== e.id);
			i.length && n.set(t, i);
		}
		let r = en([e, ...t]);
		this.state.turns = r.turns;
		for (let [e, t] of r.experiments) n.set(e, [...n.get(e) ?? [], ...t]);
		this.state.experiments = n;
	}
	async walkEvents(e) {
		let t = [], n = [], r = 0;
		for (let i = 0; i < 20; i++) {
			let i = await Pe(this.ep, e, r);
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
		let n = $t(e);
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
			Ne(n, r).catch(() => null),
			this.walkEvents(r).catch(() => null),
			N(n, r).catch(() => null),
			Le(n, r).then((e) => Array.isArray(e.spans) ? e.spans : null).catch(() => null),
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
				r && r !== n.status && (e.tried.delete(t), e.expanded.has(t) ? this.expandChild(t, !0).catch(q) : e.children.delete(t));
			}
			a && (rn(e, a.events), e.gaps = a.gaps, e.capped = a.capped), o && (e.transcript = o), s && (e.spans = s), c && (e.requests = c), e.done || e.capped ? e.held.length = 0 : this.drain(e, r, () => this.state.turn === e && !e.done), e.recheck && !e.done && (e.recheck = !1, this.catchUp(e, r, () => this.state.turn === e && !e.done).catch(q)), this.dress(e), this.emit();
		}
	}
	async readRequests(e) {
		if (!this.state.meta?.capabilities.includes("requests")) return null;
		if (T(this.ep.token) === "read") return {
			badge: "hidden",
			reason: E.hidden.reason,
			fix: E.hidden.fix,
			steps: /* @__PURE__ */ new Map()
		};
		try {
			let t = await Ie(this.ep, e);
			return {
				badge: t.badge,
				reason: t.reason,
				fix: t.fix,
				truncated: t.truncated,
				steps: ve(t.requests)
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
		for (let i = 0; i < r.length; i++) if (an(e, r[i]) === "gap") {
			e.held.push(...r.slice(i)), this.catchUp(e, t, n).catch(q);
			return;
		}
	}
	liveInto(e, t, n, r) {
		if (e.loading || e.reading) {
			e.held.push(n);
			return;
		}
		let i = an(e, n);
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
					let i = await Pe(this.ep, t, r);
					if (!n()) return;
					if (Array.isArray(i.events)) for (let t of i.events) t && typeof t.pos == "number" && !e.seen.has(t.pos) && (e.seen.add(t.pos), e.events.push(t), nn(e.feed, t.event, t.pos, t.attrs), t.pos > e.pos && (e.pos = t.pos), e.stale = !0);
					if (typeof i.next_after != "number" || i.next_after <= r) break;
					r = i.next_after;
				}
			} while (r());
		} catch {} finally {
			e.reading = !1;
		}
		if (n() && !i()) {
			for (let t of e.held.splice(0)) an(e, t, !0);
			this.emit(!0);
		}
	}
	dress(e) {
		e.stale = !1;
		let t = this.rowOf(e.id)?.status ?? e.doc?.status;
		if (e.folded = on(K(e.feed), e.transcript, e.done || !!t && t !== "running"), e.doc && Array.isArray(e.doc.children)) try {
			Ze(e.folded, e.doc.children);
		} catch {}
	}
	follow(e) {
		let t = e.id;
		this.runSub?.close(), this.runSub = F(this.ep, {
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
				this.runSub = void 0, !(this.state.turn !== e || e.done) && t !== "expired" && (t === "overflow" ? this.resumeTurn(e).catch(q) : this.retry("run", () => this.resumeTurn(e)));
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
			return;
		}
		let a = R();
		G(a, /* @__PURE__ */ new Set(), i.events);
		let [o, s, c] = await Promise.all([
			N(this.ep, e).catch(() => null),
			Ne(this.ep, e).catch(() => null),
			this.readRequests(e)
		]);
		if (r !== this.loadSeq || this.disposed || this.state.turn !== n) return;
		let l = n.doc?.children.find((t) => t.id === e)?.status ?? "running", u = on(K(a), o, l !== "running");
		if (s && Array.isArray(s.children)) try {
			Ze(u, s.children);
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
			let e = await ze(this.ep);
			i = Array.isArray(e.runtimes) ? e.runtimes : [];
		} catch (t) {
			!this.disposed && r === this.loadSeq && this.setExperimentError(J(t), e);
			return;
		}
		if (this.disposed || r !== this.loadSeq) return;
		this.state.runtimes = i;
		let a = Wt(i, n.agent), o = a?.agents.find((e) => e.name === n.agent);
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
			input: t === 0 && c && c.id === e ? zt(c.transcript) : "",
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
				input: t.input || (n && n.id === e ? zt(n.transcript) : "")
			};
		}
		await this.runExperiment();
	}
	async runExperiment() {
		let e = this.state.drawer;
		if (!e || this.posting) return;
		let t = Ht(e);
		if (t) {
			this.setExperimentError(t, e.runId);
			return;
		}
		this.posting = !0;
		let n = this.loadSeq, r;
		try {
			r = await Be(this.ep, Ut(e, this.publicId));
		} catch (t) {
			!this.disposed && n === this.loadSeq && this.setExperimentError(J(t), e.runId);
			return;
		} finally {
			this.posting = !1;
		}
		if (this.disposed || n !== this.loadSeq) return;
		let i = this.state.experiments.get(e.runId)?.length ?? 0, a = this.state.turn, o = a && a.id === e.runId ? Bt(a.transcript) ?? U(a.folded) : {
			text: "",
			calls: []
		};
		this.expSub?.close(), this.expSub = void 0;
		let s = cn(e.runId, Vt(e.runId, i), o);
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
				let a = R();
				G(a, /* @__PURE__ */ new Set(), i.events);
				let o = K(a).pending;
				if (!o.some((t) => t.id === e)) {
					r.folded.pending = o, r.error = `call ${e} is not pending on ${r.runID} — nothing was sent`, this.emit();
					return;
				}
				let s;
				try {
					s = await He(this.ep, r.runID, {
						call_id: e,
						decision: t,
						content: n
					});
				} catch (e) {
					r.error = J(e), this.emit();
					let t = await this.walkEvents(r.runID).catch(() => null);
					if (t && !this.left(r)) {
						let e = R();
						G(e, /* @__PURE__ */ new Set(), t.events), r.folded.pending = K(e).pending, this.emit();
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
		let n = Bt(await N(this.ep, e).catch(() => null));
		if (!n) {
			let t = R();
			try {
				G(t, /* @__PURE__ */ new Set(), (await this.walkEvents(e)).events);
			} catch {}
			n = U(K(t));
		}
		this.left(t) || (this.compareWords.set(e, n), this.emit());
	}
	compareWords = /* @__PURE__ */ new Map();
	async setBreakpoints(e) {
		let t = this.state.drawer;
		if (!t) return;
		let n = this.loadSeq, r;
		try {
			r = await Ue(this.ep, t.runtimeId, e);
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
				await We(this.ep, t.runID, e);
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
			let n = cn(t ?? this.state.selected, "—", {
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
				r = await Ve(this.ep, e.commandID);
			} catch (r) {
				if (this.left(e)) return;
				let i = r instanceof j && [
					401,
					403,
					404,
					410
				].includes(r.status);
				if (i || ++t >= Xt) {
					e.state = "lost", e.error = i ? J(r) : "Studio stopped answering — the command's state is unknown", this.emit();
					return;
				}
				this.after(Yt, () => void n().catch(q));
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
				this.after(Yt, () => void n().catch(q));
			}
		};
		n().catch(q);
	}
	followExperiment(e, t) {
		let n = R();
		e.runID = t, e.row = null, e.events = [], e.seen = /* @__PURE__ */ new Set(), e.feed = n, e.folded = K(n), e.pos = -1, e.held = [], e.reading = !1, e.recheck = !1, e.loading = !1, e.stale = !1, e.ready = !1, e.words = null, e.deciding = null, e.decided = {}, this.retries.exp = 0, this.openExperimentStream(e);
	}
	openExperimentStream(e) {
		let t = e.runID, n = () => !this.left(e) && e.runID === t && !e.ready;
		this.expSub?.close(), this.expSub = F(this.ep, {
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
					this.state.result !== e || e.runID !== t || e.ready || (await this.loadExperiment(e), !(this.left(e) || e.runID !== t || ln(e)) && (e.state === "queued" || e.state === "accepted") && this.openExperimentStream(e));
				};
				n !== "expired" && (n === "overflow" ? r().catch(q) : this.retry("exp", r));
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
				N(this.ep, t).catch(() => null),
				Ne(this.ep, t).catch(() => null)
			]);
		} finally {
			e.loading = !1;
		}
		if (this.disposed || this.state.result !== e || e.runID !== t) return null;
		n && n.events.length && rn(e, n.events);
		let a = () => !this.left(e) && e.runID === t && !e.ready;
		return this.drain(e, t, a), e.recheck && (e.recheck = !1, this.catchUp(e, t, a).catch(q)), i && (e.row = i), e.folded = on(K(e.feed), r, !!e.row && e.row.status !== "running"), e.stale = !1, this.emit(), { transcript: r };
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
		if (!n || ln(e)) return;
		let r = e.row;
		if ((!r || r.status === "running" || r.status === "succeeded" && !e.folded.finished) && t < 14) {
			this.after(1e3, () => void this.settleExperiment(e, t + 1).catch(q));
			return;
		}
		e.ready = !0, e.words = Bt(n.transcript) ?? U(e.folded), this.expSub?.close(), this.expSub = void 0, this.emit();
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
				t?.stale && (t.stale = !1, t.folded = K(t.feed)), this.notify(this.state);
			});
		}
	}
	dispose() {
		this.disposed = !0, this.raf && cancelAnimationFrame(this.raf), this.raf = 0, this.clearTimers(), this.scopeSub?.close(), this.runSub?.close(), this.expSub?.close(), this.devSub?.close(), this.scopeSub = this.runSub = this.expSub = this.devSub = void 0;
	}
};
function cn(e, t, n) {
	let r = R();
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
		folded: K(r),
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
function ln(e) {
	return e.ready;
}
function J(e) {
	return e instanceof Error ? e.message : String(e);
}
function un(e) {
	let t = {};
	return e.session && (t.session = e.session), e.flow && (t.flow = e.flow), e.run && (t.run = e.run), t;
}
//#endregion
//#region src/lib/links.ts
function dn(e) {
	return typeof e == "number" && Number.isInteger(e) && e >= 0 ? e : void 0;
}
function fn(e, t = {}) {
	let n = dn(t.step), r = {};
	n !== void 0 && (r.step = n), t.view && t.view !== "trace" && (r.view = t.view), t.call && (t.resumed || n !== void 0) ? r.sel = `c:${t.resumed ? "resume" : String(n)}:${t.call}` : t.span && (r.sel = `t:${t.span}`), t.axis === "time" ? r.axis = "time" : t.axis === "events" && (r.axis = "events");
	let i = dn(t.t);
	return i !== void 0 && (r.t = i), {
		to: "/runs/$id",
		params: { id: e },
		search: r
	};
}
function pn(e) {
	return {
		to: "/sessions/$id",
		params: { id: e },
		search: {}
	};
}
function mn(e, t = {}) {
	return {
		to: "/traces/$id",
		params: { id: e },
		search: t.span ? { span: t.span } : {}
	};
}
function hn(e = {}) {
	let t = new URLSearchParams();
	for (let [n, r] of Object.entries(e)) r !== void 0 && r !== "" && (n !== "step" || dn(r) !== void 0) && t.set(n, String(r));
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
function gn(e) {
	if (typeof e == "string") try {
		return JSON.parse(e), JSON.stringify(e);
	} catch {
		return e;
	}
	return typeof e == "object" ? JSON.stringify(e) : String(e);
}
function _n(e) {
	let t = e.to.replace(/^\//, "");
	"params" in e && (t = t.replace("$id", encodeURIComponent(e.params.id)));
	let n = new URLSearchParams();
	for (let [t, r] of Object.entries(e.search)) r !== void 0 && n.set(t, gn(r));
	let r = n.toString(), i = "hash" in e && e.hash ? `#${e.hash}` : "";
	return `${t}${r ? `?${r}` : ""}${i}`;
}
function Y(e, t) {
	return new URL(_n(t), e).toString();
}
//#endregion
//#region src/panel/element.ts
function vn(e, t) {
	return e.meta?.capabilities.includes(t) ?? !1;
}
function yn(e) {
	return e.status === "succeeded" && e.pending > 0 ? "parked" : e.status;
}
function bn(e, t, n) {
	return Y(e, fn(t, n === void 0 ? {} : {
		step: n,
		view: "story"
	}));
}
var xn = null;
function Sn(e) {
	try {
		if ("adoptedStyleSheets" in e && typeof CSSStyleSheet == "function") {
			if (!xn) {
				let e = new CSSStyleSheet();
				e.replaceSync(It), xn = e;
			}
			e.adoptedStyleSheets = [xn];
			return;
		}
	} catch {}
	e.append(H("style", void 0, It));
}
var X = () => {}, Cn = 3e3;
function Z(e) {
	return r({
		publicId: e.publicId,
		session: e.session,
		flow: e.flow
	});
}
var wn = [
	"data-scope=\"pub_…\" on the panel's <script> tag (or <weft-devtools>)",
	"scope(\"pub_…\") from @weftgo/devtools",
	"data-weft-scope=\"pub_…\" on the chat's element",
	"scope.Header(h, …) on the app's handler (Go, package weft/scope)"
], Tn = "no conversation detected on this page";
function En() {
	try {
		return typeof window.matchMedia == "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
	} catch {
		return !1;
	}
}
var Dn = class extends HTMLElement {
	static observedAttributes = [
		"data-endpoint",
		"data-scope",
		"data-public-id",
		"data-token",
		"data-detect",
		"data-position",
		"data-open",
		"data-auto"
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
	open;
	opened = !1;
	keys = !1;
	body;
	last = W();
	held = !1;
	composing = !1;
	dirty = !1;
	holdTimer = null;
	scratch = /* @__PURE__ */ new Map();
	openKeys = /* @__PURE__ */ new Set();
	onKey = (e) => this.keydown(e);
	onURL = () => {
		let e = w();
		(e ? r(e) : "") !== (this.cfg.urlScope ? r(this.cfg.urlScope) : "") && this.rescan();
	};
	onRelease = () => this.release();
	constructor() {
		super(), this.cfg = b(this), this.open = this.cfg.open, this.shadow = this.attachShadow({ mode: "open" }), this.body = H("div", "weft-root"), Sn(this.shadow), this.shadow.append(this.body), this.shadow.addEventListener("pointerdown", () => this.hold()), this.shadow.addEventListener("compositionstart", () => {
			this.composing = !0;
		}), this.shadow.addEventListener("compositionend", () => {
			this.composing = !1, this.flush();
		}), this.shadow.addEventListener("focusout", () => {
			this.composing && (this.composing = !1, this.flush());
		}), this.render(this.last);
	}
	connectedCallback() {
		this.cfg = b(this), this.opened || (this.opened = !0, this.open = this.cfg.open), window.addEventListener("keydown", this.onKey), window.addEventListener("pointerup", this.onRelease, !0), window.addEventListener("pointercancel", this.onRelease, !0), window.addEventListener("hashchange", this.onURL, { passive: !0 }), window.addEventListener("popstate", this.onURL, { passive: !0 }), this.schedule();
	}
	disconnectedCallback() {
		window.removeEventListener("keydown", this.onKey), window.removeEventListener("pointerup", this.onRelease, !0), window.removeEventListener("pointercancel", this.onRelease, !0), window.removeEventListener("hashchange", this.onURL), window.removeEventListener("popstate", this.onURL), this.holdTimer && clearTimeout(this.holdTimer), this.holdTimer = null, this.held = this.composing = !1, this.startSeq++, this.probe?.abort(), this.probe = null, this.model?.dispose(), this.model = null, this.conn = null, this.ready = !1, this.dropRung(), this.dropMarkers();
	}
	detectWord() {
		let e = this.rung ? this.markerRung ? "headers+markers" : "headers" : this.markerRung ? "markers" : "";
		return this.followsURL() ? e ? `url+${e}` : "url" : e || (this.cfg.detect === "off" ? "off" : this.cfg.scopeExplicit ? "explicit" : "none");
	}
	followsURL() {
		return !!this.cfg.urlScope && this.scopeNow() === this.cfg.urlScope;
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
		return [r(this.scopeNow()), ...this.conversations().map((e) => e.key + e.source)].join("|");
	}
	onMarkers(e) {
		let t = this.markers, n = (e) => r(e.scope);
		if (e.length === t.length && e.every((e, r) => e.element === t[r].element && n(e) === n(t[r]))) return;
		let i = this.markerSig(), a = this.marked ? Z(this.marked) : "";
		a && e.length === 1 && !e.some((e) => Z(e.scope) === a) && !e.some((e) => t.some((t) => t.element === e.element)) && (this.marked = null), this.markers = e, this.follow(), this.markerSig() !== i && this.rescopeSoon();
	}
	onMarkerFocus(e) {
		if (e === this.lastFocused && !this.chosen) return;
		let t = this.markerSig();
		this.lastFocused = e, this.chosen = null, this.follow(), this.markerSig() !== t && this.rescopeSoon();
	}
	follow() {
		let e = this.markers, t = (t) => t ? e.find((e) => e.element === t) : void 0, n = this.marked ? Z(this.marked) : "", r = t(Ot(document.activeElement)) ?? t(this.lastFocused) ?? e.find((e) => n && Z(e.scope) === n), i = Z(this.cfg.scope);
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
		let e = this.isConnected && this.ready && !this.dormant && ie(this.cfg);
		e && !this.rung ? (this.notRestored = !1, this.rung = u({
			onScope: (e, t) => this.onDetected(e, t),
			ignore: (e) => !!this.base && e.startsWith(this.base)
		})) : !e && this.rung && this.dropRung();
		let t = this.isConnected && !this.dormant && ae(this.cfg);
		t && !this.markerRung ? this.markerRung = jt({
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
		let n = Z(e);
		this.byPath.set(t, e);
		let i = Date.now(), a = this.seen.find((e) => e.key === n);
		a ? (a.scope = e, a.path = t, a.at = i) : (this.seen.push({
			scope: e,
			key: n,
			path: t,
			at: i
		}), this.seen.length > 20 && this.seen.shift());
		let o = this.detected;
		if (o && Z(o) !== n && this.followedPath && t !== this.followedPath) return;
		(!o || Z(o) !== n) && (this.followedPath = t);
		let s = o ? r(o) : "";
		this.detected = e, r(e) !== s && !this.cfg.scopeExplicit && this.schedule();
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
		this.cfg = b(this), this.isConnected && (this.syncDetect(), this.schedule());
	}
	attributeChangedCallback(e, t, n) {
		t !== n && (this.cfg = b(this), this.isConnected && (this.syncDetect(), this.schedule()));
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
				this.model.publicId !== n.publicId && this.scratch.clear(), t.scope = i;
				let r = this.forceNext || e.scopeExplicit && n === e.scope && !this.explicitMarked;
				this.model.rescope(n, { force: r }).catch(X);
			}
			this.forceNext = !1, this.render(this.last);
			return;
		}
		this.start().catch(X);
	}
	async start(e = !1) {
		let t = ++this.startSeq, n = this.cfg;
		this.ready = !1, this.dropRung(), this.model?.dispose(), this.model = null, this.conn = null, this.probe?.abort(), this.probe = null, this.scratch.clear(), e && this.dormant && this.unreachable !== null ? (this.checking = !0, this.render(W())) : (this.dormant = !1, this.unreachable = null, this.checking = !1, this.render(W()));
		let i = !1, a = n.endpoint;
		if (n.configURL) {
			let e = new AbortController();
			this.probe = e;
			let r = setTimeout(() => e.abort(), Cn);
			try {
				a = await ne(n.configURL, e.signal) || n.endpoint;
			} finally {
				clearTimeout(r), this.probe === e && (this.probe = null);
			}
			if (t !== this.startSeq) return;
		}
		if (a) {
			let e = this.scopeNow();
			this.forceNext = !1;
			let o = new sn({
				base: a,
				token: n.token
			}, e, (e) => this.render(e));
			this.model = o, this.conn = {
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
		this.cfg = b(this), this.start(!0).catch(X);
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
		if (this.dormant) {
			if (this.unreachable !== null) {
				let e = H("div", "weft-unreachable", void 0, { role: "status" });
				if (e.append(this.unreachable ? "Studio not reachable at " : "Studio not reachable: no http(s) endpoint", ...this.unreachable ? [H("span", "weft-unreachable-at", this.unreachable)] : [], " · "), this.checking) e.append(H("span", "weft-checking", "checking…"));
				else {
					let t = H("button", "weft-retry", "retry", {
						type: "button",
						title: "ask Studio again"
					});
					t.addEventListener("click", () => this.retry()), e.appendChild(t);
				}
				n.push(e);
			}
		} else if (!this.open) n.push(this.pill(e));
		else if (!e.gone) {
			let t = H("div", `weft-dock weft-${this.cfg.position} weft-open`);
			t.appendChild(this.header(e)), this.howTo && !this.model?.publicId && t.appendChild(this.howToBox());
			let r = H("div", "weft-cols");
			r.appendChild(this.turnList(e)), r.appendChild(this.main(e)), t.appendChild(r), t.appendChild(this.footer(e)), e.raw && e.turn && t.appendChild(this.rawView(e.turn)), this.keys && t.appendChild(this.shortcuts()), n.push(t);
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
	pill(e) {
		let t = e.live ? e.turns.find((e) => e.status === "running") : void 0;
		if (!t) {
			let e = H("button", `weft-fab weft-fab-${this.cfg.position}`, "devtools", { title: "weft devtools — Alt+W" });
			return e.addEventListener("click", () => this.toggle()), e;
		}
		let n = e.turn?.id === t.id ? e.turn.folded.steps.length : 0, r = Math.max(n, t.steps), i = `weft devtools · running${r ? `, step ${r}` : ""}`, a = H("button", `weft-fab weft-fab-${this.cfg.position} weft-fab-running${En() ? "" : " weft-fab-pulse"}`, [document.createTextNode("devtools"), H("span", "weft-fab-count", r ? ` ● ${r}` : " ●")], {
			title: `${i} — Alt+W`,
			"aria-label": i
		});
		return a.addEventListener("click", () => this.toggle()), a;
	}
	howToBox() {
		let e = H("div", "weft-howto");
		for (let t of wn) e.appendChild(H("code", void 0, t));
		return e;
	}
	go(e) {
		e?.catch(X);
	}
	shortcuts() {
		let e = H("div", "weft-keys"), t = H("dl");
		for (let [e, n] of [
			["Alt+W", "toggle the dock (Ctrl+Shift+W too, where the browser delivers it)"],
			["r", "raw JSON of the open turn"],
			["Esc", "close"],
			["?", "this list"]
		]) t.appendChild(H("dt", void 0, e)), t.appendChild(H("dd", void 0, n));
		return e.appendChild(t), e;
	}
	header(e) {
		let t = H("div", "weft-head"), n = e.turns.some((e) => e.status === "running");
		t.appendChild(H("span", `weft-dot${e.live ? n ? " weft-run" : " weft-on" : ""}`, void 0, { title: e.live ? "live" : "history" }));
		let r = e.session?.agent ?? e.turns.at(0)?.agent ?? "", i = this.model?.publicId || e.session?.public_id || "", a = i ? `${r ? r + " · " : ""}${i}` : Tn;
		if (t.appendChild(H("span", "weft-title", a, { title: i ? a : `${a}: the newest runs are shown` })), !i) {
			let e = H("button", `weft-btn weft-howto-btn${this.howTo ? " weft-active" : ""}`, "how to scope", {
				type: "button",
				"aria-expanded": String(this.howTo),
				title: "the one-line ways to scope the panel to your conversation"
			});
			e.addEventListener("click", () => {
				this.howTo = !this.howTo, this.render(this.last);
			}), t.append(" · ", e);
		}
		let o = this.switcher(e);
		o && t.appendChild(o);
		let s = this.model?.narrowing ?? {};
		s.session && t.appendChild(H("span", "weft-chip weft-scope-chip", `session ${s.session}`, { title: "the turn list is narrowed to this session" })), s.flow && t.appendChild(H("span", "weft-chip weft-scope-chip", `flow ${s.flow}`, { title: "the scope's flow — carried, filters nothing yet" })), t.appendChild(H("span", "weft-grow"));
		let c = e.turns.reduce((e, t) => e + t.usage.input_tokens, 0), l = e.turns.reduce((e, t) => e + t.usage.output_tokens, 0), u = `${e.turns.length}${e.turnsCapped ? "+" : ""} turns · ${z(c)}→${z(l)} tok`;
		if (t.appendChild(H("span", void 0, u, { title: u })), e.turns.length && e.selected) {
			let n = H("a", "weft-btn", "⤢", {
				href: bn(this.base, e.selected, kn(e)),
				target: "_blank",
				rel: "noopener",
				title: "open in Studio (run, and the step you are reading)"
			});
			n.style.textDecoration = "none", t.appendChild(n);
		}
		let d = H("button", `weft-btn${e.raw ? " weft-active" : ""}`, "raw", { title: "the JSON, one keypress away (r)" });
		d.addEventListener("click", () => this.toggleRaw()), t.appendChild(d);
		let f = H("button", "weft-btn", "–", { title: "collapse (Alt+W)" });
		return f.addEventListener("click", () => this.toggle()), t.appendChild(f), t;
	}
	switcher(e) {
		let t = this.conversations(), n = Z(this.scopeNow()), r = t.some((e) => e.key === n);
		if (t.length < (r ? 2 : 1)) return null;
		let i = H("select", "weft-switch", void 0, {
			"aria-label": "conversation",
			"data-weft-k": "switch"
		}), a = (e, t, n) => {
			let r = H("option", void 0, e, { value: t });
			return r.selected = n, i.appendChild(r), r;
		}, o = e.live ? "● " : "○ ";
		return r || (a(`${o}${n || "latest (dev)"} · not on the page`, "", !0).disabled = !0), t.forEach((e, t) => {
			let { publicId: r, session: i, flow: s } = e.scope, c = [
				r,
				i && `session ${i}`,
				s && `flow ${s}`,
				e.source
			].filter(Boolean).join(" · ");
			a(e.key === n ? o + c : c, String(t), e.key === n).setAttribute("data-weft-source", e.source);
		}), i.addEventListener("change", () => {
			let e = i.value ? t.at(Number(i.value)) : void 0;
			e && e.key !== n && this.choose(e);
		}), i;
	}
	turnList(e) {
		let t = H("div", "weft-turns");
		e.pinMissing && t.appendChild(H("div", "weft-note weft-pin-missing", `run ${e.pinMissing} not in this conversation`));
		let n = this.model?.narrowing.session;
		if (n && e.sessionUnrecorded && t.appendChild(H("div", "weft-note", `session ${n}: these runs carry no session id — not narrowed`)), !this.model?.publicId && e.devRefused ? t.appendChild(H("div", "weft-note weft-dev-poll", "streaming needs the server token · polling")) : e.devAgent && e.turns.some((t) => t.agent !== e.devAgent) && t.appendChild(H("div", "weft-note weft-dev-poll", `live: agent ${e.devAgent} · the other agents' runs every ${Zt / 1e3} s`)), !e.turns.length && !e.experiments.size) {
			let e = n ? `no turns of session ${n} yet` : this.model?.publicId ? "no turns yet — run your app" : "no runs yet (dev)";
			return t.appendChild(H("div", "weft-splash", e)), t;
		}
		let r = /* @__PURE__ */ new Set();
		for (let n of e.turns) {
			r.add(n.id), t.appendChild(this.turnRow(n, e.selected));
			let i = e.experiments.get(n.id) ?? [];
			if (i.length) {
				let n = H("div", "weft-expts");
				for (let t of i) n.appendChild(this.turnRow(t, e.selected));
				t.appendChild(n);
			}
		}
		let i = [];
		for (let [t, n] of e.experiments) r.has(t) || i.push(...n);
		if (i.length) {
			let n = H("div", "weft-expts");
			for (let t of i) n.appendChild(this.turnRow(t, e.selected));
			t.appendChild(n);
		}
		return e.turnsCapped && t.appendChild(H("div", "weft-note", "the newest 50 runs — older ones are in Studio (⤢)")), t;
	}
	turnRow(e, t) {
		let n = yn(e), r = H("button", `weft-turn${e.id === t ? " weft-sel" : ""}`), i = H("div", "weft-row1", [
			H("span", `weft-chip weft-${n}`, n),
			H("span", "weft-id", e.id, { title: e.id }),
			H("span", "weft-when", dt(e.last_seen || e.started))
		]), a = e.usage, o = H("div", "weft-row2", [
			H("span", void 0, e.model.name ? `${e.model.provider}/${e.model.name}` : ""),
			H("span", void 0, `${e.steps} steps`),
			H("span", void 0, `${z(a.input_tokens)}→${z(a.output_tokens)}`),
			H("span", void 0, ft(e.started, e.finished) || "…")
		]);
		return r.append(i, o), e.err && r.appendChild(H("div", "weft-reason", e.err)), r.addEventListener("click", () => this.go(this.model?.select(e.id, !0))), r;
	}
	main(e) {
		let t = H("div", "weft-main");
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
		}), e.tooNew && e.meta ? (t.appendChild(H("div", "weft-note weft-warn", [H("span", "weft-warn", "Studio is newer than this panel; update panel.js"), H("span", void 0, `studio_version ${e.meta.studio_version} · panel built for ${Gt()}`)])), t) : e.turn ? (t.appendChild(this.turnView(e)), t.appendChild(this.playgroundArea(e)), t) : (t.appendChild(H("div", "weft-splash", "select a turn")), t);
	}
	playgroundArea(e) {
		let t = H("div"), n = e.turn?.id ?? "";
		return e.result && e.result.sourceRunID === n && t.appendChild(this.experimentResult(e)), this.canAct(e) && e.drawer && e.drawer.runId === n && t.appendChild(this.drawer(e)), t;
	}
	canAct(e) {
		return vn(e, "playground") && T(this.cfg.token) !== "read";
	}
	field(e, t) {
		return e.setAttribute("data-weft-k", t), e;
	}
	drawer(e) {
		let t = e.drawer;
		if (!t) return H("div");
		let n = e.runtimes.find((e) => e.id === t.runtimeId)?.agents.find((e) => e.name === t.agent), r = H("div", "weft-step weft-drawer"), i = H("div", "weft-step-h", [H("span", void 0, `Experiment · ${t.agent}${t.step > 0 ? ` · continue from step ${t.step}` : ""}`), H("span", "weft-grow")]), a = H("button", "weft-btn", "–", { title: "close the drawer" });
		a.addEventListener("click", () => this.model?.closeExperiment()), i.appendChild(a), r.appendChild(i);
		let o = H("div", "weft-step-b"), s = H("label", "weft-field", [H("span", void 0, "System prompt")]), c = this.field(H("textarea", "weft-input"), "prompt");
		c.rows = 3, c.value = t.instructions, c.addEventListener("input", () => this.model?.setDraft({ instructions: c.value }, !0));
		let l = H("button", "weft-btn", "↺", { title: "reset to the registered prompt" });
		if (l.addEventListener("click", () => {
			this.model?.setDraft({ instructions: t.registeredInstructions });
		}), s.append(c, l), o.appendChild(s), n?.tools.length) {
			let e = H("div", "weft-field", [H("span", void 0, "Tools")]);
			for (let r of n.tools) {
				let n = H("input");
				n.type = "checkbox", n.checked = t.tools[r.name] ?? !0, n.addEventListener("change", () => this.model?.setDraft({ tools: {
					...this.model.state.drawer?.tools ?? t.tools,
					[r.name]: n.checked
				} }));
				let i = H("label", "weft-tool", [n, H("span", void 0, r.name)]);
				(r.side_effects === "never" || !r.side_effects) && i.appendChild(H("span", "weft-badge weft-warn-badge", "⚠", { title: "side-effect tool (ReplayPolicy never): its calls substitute or park — never re-fire silently; only side effects: allow runs it for real, and only if the app opted it in" })), e.appendChild(i);
			}
			o.appendChild(e);
		}
		let u = H("div", "weft-fields"), d = H("select", "weft-input"), f = e.turn?.doc?.model.name ?? "", p = H("option", void 0, `model: ${f || "—"}`);
		p.value = "", d.appendChild(p);
		for (let e of n?.models ?? []) {
			if (e === f) continue;
			let t = H("option", void 0, e);
			t.value = e, d.appendChild(t);
		}
		d.value = t.model, d.addEventListener("change", () => this.model?.setDraft({ model: d.value })), u.appendChild(d);
		let m = H("select", "weft-input"), h = H("option", void 0, "thinking: default");
		h.value = "", m.appendChild(h);
		for (let e of [
			"off",
			"low",
			"medium",
			"high"
		]) {
			let t = H("option", void 0, e);
			t.value = e, m.appendChild(t);
		}
		if (m.value = t.thinking, m.addEventListener("change", () => this.model?.setDraft({ thinking: m.value })), u.appendChild(m), o.appendChild(u), t.step === 0) {
			let e = H("label", "weft-field", [H("span", void 0, "Input (replaces the user message)")]), n = this.field(H("textarea", "weft-input"), "input");
			n.rows = 2, n.value = t.input, n.addEventListener("input", () => this.model?.setDraft({ input: n.value }, !0)), e.appendChild(n), o.appendChild(e);
		}
		let g = H("div", "weft-fields"), _ = H("select", "weft-input");
		_.title = "How side-effect tools behave in the re-run. ReplaySafe tools always run; the others substitute, park, or — under allow, if the app opted them in — run for real.";
		let ee = H("option", void 0, "side effects: substitute", { title: "a side-effect call the source recorded is answered from the record; any other call parks for you" });
		ee.value = "", _.appendChild(ee);
		let v = H("option", void 0, "park", { title: "every side-effect call parks for you; nothing is answered from the record" });
		v.value = "park", _.appendChild(v);
		let te = H("option", void 0, "allow — runs the tools this app opted in (AllowSideEffects) for real", { title: "refused unless every tool left on is opted in or ReplaySafe" });
		te.value = "allow", _.appendChild(te), _.value = t.sideEffects === "substitute" ? "" : t.sideEffects, _.addEventListener("change", () => this.model?.setDraft({ sideEffects: _.value })), g.appendChild(_);
		let y = H("select", "weft-input"), b = H("option", void 0, "engine: live");
		b.value = "live", y.appendChild(b);
		let ne = H("option", void 0, "scripted (zero tokens)");
		ne.value = "scripted", y.appendChild(ne), y.value = t.engine, y.addEventListener("change", () => this.model?.setDraft({ engine: y.value })), g.appendChild(y);
		let x = H("select", "weft-input"), S = H("option", void 0, "thread: ephemeral");
		S.value = "ephemeral", x.appendChild(S);
		let C = H("option", void 0, "fork (new session)");
		if (C.value = "fork", x.appendChild(C), x.value = t.thread, x.title = "fork continues the conversation in a new session (needs an input)", x.addEventListener("change", () => this.model?.setDraft({ thread: x.value })), g.appendChild(x), o.appendChild(g), vn(e, "breakpoints") && T(this.cfg.token) === "" && n?.tools.length) {
			let t = H("div", "weft-field");
			t.appendChild(H("span", void 0, "Break on (parks every run)", { title: "applies to runs this runtime starts — the app's own turns are viewer-only (PQ7)" }));
			for (let r of n.tools) {
				let n = H("input");
				n.type = "checkbox", n.checked = e.breakpoints.includes(r.name), n.addEventListener("change", () => {
					let t = (this.model?.state.breakpoints ?? e.breakpoints).filter((e) => e !== r.name);
					n.checked && t.push(r.name), t.sort(), this.go(this.model?.setBreakpoints(t));
				}), t.appendChild(H("label", "weft-tool", [n, H("span", void 0, r.name)]));
			}
			o.appendChild(t);
		}
		let re = e.turn;
		if (t.step > 0 && re) {
			let e = () => this.model?.state.drawer ?? t, n = H("div", "weft-field");
			n.appendChild(H("span", void 0, `Transcript edits (steps 0..${t.step - 1} are kept)`));
			for (let [r, i] of re.folded.steps.entries()) {
				if (r >= t.step) break;
				for (let t of i.toolCalls) {
					if (!t.result) continue;
					let i = H("label", "weft-edit");
					i.appendChild(H("span", void 0, `step ${r} · ${t.name} →`));
					let a = this.field(H("input", "weft-input"), `edit:${r}:${t.callId}`);
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
					let t = H("label", "weft-edit");
					t.appendChild(H("span", void 0, `step ${r} · reply`));
					let i = this.field(H("textarea", "weft-input"), `edit:${r}`);
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
		let w = H("button", "weft-run-btn", "Run experiment ▶", { title: "POST /api/playground/runs — the runtime in your app executes it" });
		return w.addEventListener("click", () => this.go(this.model?.runExperiment())), o.appendChild(w), r.appendChild(o), r;
	}
	experimentResult(e) {
		let t = e.result;
		if (!t) return H("div");
		let n = H("div", "weft-step weft-xres"), r = t.row?.usage, i = [
			t.state,
			r ? `${z(r.input_tokens)}→${z(r.output_tokens)} tok` : "",
			t.row ? ft(t.row.started, t.row.finished) : ""
		].filter(Boolean).join(" · "), a = H("div", "weft-step-h", [
			H("span", void 0, `Result · ${t.label}`),
			H("span", void 0, i),
			H("span", "weft-grow")
		]), o = H("button", "weft-btn", "keep as prompt ⤴", { title: "copy the edited prompt (weft/prompt versions are post-v1, PQ2)" });
		o.addEventListener("click", () => {
			let e = this.model?.state.drawer?.instructions ?? "";
			try {
				navigator.clipboard?.writeText(e).catch(X);
			} catch {}
		}), a.appendChild(o);
		let s = H("a", "weft-btn", "save as fixture", {
			href: Y(this.base, hn(t.runID ? { run: t.runID } : {})),
			target: "_blank",
			rel: "noopener",
			title: "hand off to Studio: the run's records as wefttest replay fixtures (D4)"
		});
		s.style.textDecoration = "none", a.appendChild(s);
		let c = H("a", "weft-btn", "compare in Studio", { title: "open the Studio playground with this run, step and the current overrides carried over" }), l = () => {
			let n = this.model?.state.drawer ?? null, r = n && n.runId === t.sourceRunID ? n : null, i = r && r.step > 0 ? r.step : e.turn ? An(e.turn.folded, e.selectedStep) : -1;
			c.setAttribute("href", Un(this.base, r, i));
		};
		l(), c.setAttribute("target", "_blank"), c.setAttribute("rel", "noopener");
		for (let e of [
			"pointerdown",
			"focus",
			"click",
			"contextmenu"
		]) c.addEventListener(e, l);
		c.style.textDecoration = "none", a.appendChild(c);
		let u = H("button", "weft-btn", "discard", { title: "clear the result pane" });
		u.addEventListener("click", () => this.model?.discardResult()), a.appendChild(u), n.appendChild(a);
		let d = H("div", "weft-step-b");
		t.error && d.appendChild(H("div", "weft-note weft-warn", t.error));
		let f = t.row?.status ?? (t.ready ? "succeeded" : "running");
		for (let e of t.folded.steps) {
			e.text && d.appendChild(H("div", void 0, e.text));
			for (let n of e.toolCalls) d.appendChild(zn(n, e.index, f, void 0, void 0, t.runID ? {
				endpoint: this.base,
				runId: t.runID
			} : void 0));
		}
		!t.folded.steps.length && !t.error && t.state === "queued" ? d.appendChild(H("div", "weft-note", "queued — waiting for the runtime to ack…")) : !t.folded.steps.length && !t.error && t.state === "accepted" && !t.runID && d.appendChild(H("div", "weft-note", "accepted — the fork's turn is running in its new session…"));
		let p = [{
			id: "",
			label: Wn(t.label)
		}, ...(e.experiments.get(t.sourceRunID) ?? []).filter((e) => e.id !== t.runID).map((e) => ({
			id: e.id,
			label: $(e.id)
		}))];
		if (p.length > 1) {
			let e = H("select", "weft-input");
			for (let t of p) {
				let n = H("option", void 0, `compare vs ${t.label || "source"}`);
				n.value = t.id, e.appendChild(n);
			}
			e.value = t.compareWith, e.addEventListener("change", () => this.go(this.model?.setCompare(e.value))), d.appendChild(e);
		}
		let m = t.ready ? t.words ?? U(t.folded) : null, h = t.compareWith ? this.model?.compareWords.get(t.compareWith) : t.source, g = t.compareWith ? $(t.compareWith) : Wn(t.label);
		if (m && h && m.text && h.text) {
			let e = H("div", "weft-diff");
			this.diffInto(e, `diff vs ${g}:`, h.text, m.text), h.calls.join("\n") !== m.calls.join("\n") && this.diffInto(e, "tool calls:", h.calls.join("\n"), m.calls.join("\n")), d.appendChild(e);
		}
		if (t.ready && t.folded.pending.length && t.runID && this.canAct(e) && d.appendChild(this.decisions(t.folded.pending, t.decided)), vn(e, "steer") && this.canAct(e) && t.state === "accepted" && t.runID) {
			let e = H("div", "weft-step");
			e.appendChild(H("div", "weft-step-h", [H("span", void 0, "steer this run")]));
			let t = H("div", "weft-step-b"), n = this.field(H("input", "weft-input"), "steer");
			n.placeholder = "a message delivered mid-flight", n.value = this.scratch.get("steer") ?? "", n.addEventListener("input", () => this.scratch.set("steer", n.value));
			let r = H("button", "weft-btn", "steer", { title: "POST /api/runs/{id}/steer (ADR 0019)" });
			r.addEventListener("click", () => {
				n.value &&= (this.go(this.model?.steer(n.value)), this.scratch.delete("steer"), "");
			}), t.append(n, r), e.appendChild(t), d.appendChild(e);
		}
		return n.appendChild(d), n;
	}
	diffInto(e, t, n, r) {
		if ((n.split("\n").length + 1) * (r.split("\n").length + 1) > 25e4) {
			e.appendChild(H("div", "weft-diff-h", `${t}  too large for the panel — compare in Studio`));
			return;
		}
		let i = qe(n, r);
		e.appendChild(H("div", "weft-diff-h", `${t}  ${Je(i)}`));
		for (let t of i) t.kind !== "same" && e.appendChild(H("div", `weft-diff-row weft-diff-${t.kind}`, `${t.kind === "add" ? "+" : "−"} ${t.text}`));
	}
	decisions(e, t) {
		let n = H("div", "weft-step");
		n.appendChild(H("div", "weft-step-h", [H("span", void 0, "awaiting decision")]));
		let r = H("div", "weft-step-b"), i = e.filter((e) => !t[e.id]).length;
		i < e.length && r.appendChild(H("div", "weft-note", `waiting for ${i} more decision${i === 1 ? "" : "s"} — the run resumes once every parked call is decided`));
		let a = {
			approve: "continue",
			deny: "skip",
			resolve: "resolve"
		};
		for (let n of e) {
			let e = H("div", "weft-call"), i = H("div", "weft-call-h", [H("span", "weft-name", n.name), H("span", "weft-args", n.args === void 0 ? "(…)" : Nt(n.args))]);
			t[n.id] && i.appendChild(H("span", "weft-badge weft-info", `decided: ${a[t[n.id]] ?? t[n.id]}`)), e.appendChild(i);
			let o = H("div", "weft-res"), s = `resolve:${n.id}`, c = this.field(H("input", "weft-input weft-resolve"), s);
			c.placeholder = "the result to resolve with", c.value = this.scratch.get(s) ?? "", c.addEventListener("input", () => this.scratch.set(s, c.value));
			let l = (e, t, n) => {
				let r = H("button", "weft-btn", e, { title: t });
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
		if (!t) return H("div");
		let n = H("div");
		n.appendChild(this.notes(t));
		let r = this.turnLinks(t);
		r && n.appendChild(r);
		let i = Pt(t.spans ?? []);
		i.length && n.appendChild(jn(i));
		let a = zt(t.transcript);
		a && n.appendChild(H("div", "weft-note", a));
		let o = mt(t.doc);
		for (let e of o.filter(V)) n.appendChild(Pn(e, o, t.transcript, {
			keys: this.openKeys,
			scope: t.id
		}));
		let s = this.rowOf(t.id);
		return n.appendChild(Mn(t.folded, s?.status ?? t.doc?.status ?? "running", t, e.selectedStep, {
			keys: this.openKeys,
			scope: t.id
		}, {
			endpoint: this.base,
			runId: t.id
		})), t.folded.pending.length && n.appendChild(this.approvals(t.folded.pending, !!s?.playground)), this.canAct(e) && n.appendChild(this.actions(e)), n;
	}
	turnLinks(e) {
		let t = this.rowOf(e.id), n = t?.session_id || e.doc?.session_id || "", r = t?.trace_id || e.doc?.trace_id || "";
		if (!n && !r) return null;
		let i = H("div", "weft-row2"), a = (e, t, n, r, i) => {
			let a = H("a", "weft-chip", e, {
				href: t,
				target: "_blank",
				rel: "noopener",
				title: n,
				[r]: i
			});
			return a.style.textDecoration = "none", a;
		};
		return n && i.appendChild(a(`session ${n}`, Y(this.base, pn(n)), "the session in Studio", "data-weft-session-link", n)), r && i.appendChild(a(`trace ${r.slice(0, 8)}`, Y(this.base, mn(r)), `the OTel trace ${r} in Studio`, "data-weft-trace-link", r)), i;
	}
	actions(e) {
		let t = e.turn;
		if (!t) return H("div");
		let n = H("div", "weft-actions"), r = H("button", "weft-btn", "✎ Experiment", { title: "open the experiment drawer, pre-filled from the registered config" });
		r.addEventListener("click", () => this.go(this.model?.openExperiment(t.id, 0))), n.appendChild(r);
		let i = H("button", "weft-btn", "↻ Re-run", { title: "re-run the whole turn with the drawer's current edits" });
		i.addEventListener("click", () => this.go(this.model?.rerun(t.id))), n.appendChild(i);
		let a = An(t.folded, e.selectedStep);
		if (a > 0) {
			let e = H("button", "weft-btn", `⎇ Continue from step ${a}`, { title: "keep the transcript through the previous step (edits apply) and run this step fresh" });
			e.addEventListener("click", () => this.go(this.model?.openExperiment(t.id, a))), n.appendChild(e);
		}
		return n;
	}
	notes(e) {
		let t = H("div");
		t.setAttribute("data-weft-turn-holes", "");
		let n = On(e, this.rowOf(e.id));
		for (let e of n) {
			let n = O(e), r = H("div", `weft-note${n.tone === "loss" ? " weft-warn" : ""}`, `${n.label} — ${n.reason}${n.fix ? ` · fix: ${n.fix}` : ""}`);
			r.setAttribute("data-weft-hole", e.hole), t.appendChild(r);
		}
		return e.capped && t.appendChild(H("div", "weft-note weft-warn", "a long run: the first 10000 events are shown — the whole story is in Studio (⤢)")), t;
	}
	approvals(e, t) {
		let n = H("div", "weft-step");
		n.appendChild(H("div", "weft-step-h", [H("span", void 0, "awaiting decision (read-only)")]));
		let r = H("div", "weft-step-b");
		for (let n of e) {
			let e = H("div", "weft-call");
			e.appendChild(H("div", "weft-call-h", [
				H("span", "weft-name", n.name),
				H("span", "weft-args", n.args === void 0 ? "(…)" : Nt(n.args)),
				H("span", "weft-badge weft-info", "parked")
			])), e.appendChild(H("div", "weft-res", t ? "an experiment's run — its decision controls are in the result pane of the turn that ran it" : "the app's own turns are viewer-only (PQ7) — decide from your app")), r.appendChild(e);
		}
		return n.appendChild(r), n;
	}
	footer(e) {
		let t = [H("span", void 0, "prompts, args and results from your app, via your Studio")];
		tn(e.turn?.folded) && t.push(H("span", void 0, " · content is stripped for this destination"));
		let n = this.detectWord();
		return t.push(H("span", "weft-detect", ` · detect: ${n}${this.rung?.chained ? " (chained)" : ""}${this.notRestored && !this.rung ? " (fetch not restored: patched after the panel)" : ""}`, { title: n.startsWith("url") ? "scope from the page URL's weft_scope" : n.includes("headers") || n === "markers" ? "reading Weft-Scope on same-origin fetches / data-weft-scope markers" : "scope from data-scope / window.__WEFT__" })), H("div", "weft-footer", t);
	}
	rawView(e) {
		return H("pre", "weft-raw", Nt({
			doc: e.doc,
			events: e.events,
			transcript: e.transcript
		}));
	}
	rowOf(e) {
		return this.model?.rowOf(e);
	}
};
function On(e, t) {
	return D(ot(e.doc, e.folded), he({
		status: t?.status ?? e.doc?.status,
		stop_reason: t?.stop_reason ?? e.doc?.stop_reason,
		gaps: e.gaps
	}));
}
function kn(e) {
	if (e.selectedStep != null) return e.selectedStep;
	let t = e.turn;
	if (t && t.id === e.selected) return ([...e.turns, ...[...e.experiments.values()].flat()].find((e) => e.id === t.id)?.status ?? t.doc?.status) === "running" ? t.folded.steps.at(-1)?.index : void 0;
}
function An(e, t) {
	return t == null ? -1 : e.steps.findIndex((e) => e.index === t);
}
function jn(e) {
	let t = H("div", "weft-wf");
	for (let n of e) {
		let e = H("div", "weft-wf-row");
		e.appendChild(H("span", "weft-wf-name", n.name, { title: n.name }));
		let r = H("span", "weft-wf-track"), i = H("span", "weft-wf-bar");
		i.style.left = `${(n.left * 100).toFixed(2)}%`, i.style.width = `${(n.width * 100).toFixed(2)}%`, r.appendChild(i), e.appendChild(r), e.appendChild(H("span", "weft-wf-ms", `${n.ms}ms`)), t.appendChild(e);
	}
	return t;
}
function Mn(e, t, n, r, i, a) {
	let o = H("div");
	e.model?.name && o.appendChild(H("div", "weft-reason", `${e.model.provider}/${e.model.name}`));
	for (let s of e.steps) o.appendChild(Nn(s, t, n, r, i, a));
	return o;
}
function Nn(e, t, n, r, i, a) {
	let o = H("div", "weft-step");
	o.setAttribute("data-weft-step", String(e.index)), r === e.index && (o.style.outline = "1px solid var(--w-accent)");
	let s = H("div", "weft-step-h", [H("span", void 0, `step ${e.index}`), H("span", "weft-grow")]), c = a?.child ? a.child.requests : n?.requests, l = c?.steps.get(e.index)?.rows ?? [], u = !c?.error && (!c?.truncated || e.index < Fn(c));
	e.finish && (s.appendChild(H("span", void 0, e.finish.reason)), s.appendChild(H("span", void 0, Kn(e.finish.usage))));
	let d = u ? St(xt(l, !!e.finish || e.toolCalls.length > 0, t === "running")) : null;
	if (d && s.appendChild(H("span", "weft-badge weft-info", d, { "data-weft-attempts": "" })), e.finish) {
		let t = wt(e.finish.latencyMs, e.finish.ttftMs, "ttft");
		t && s.appendChild(H("span", void 0, t, { "data-weft-timing": "" }));
	}
	let f = at(e, a?.child ? a.child.doc?.holes : n?.doc?.holes), p = u ? Tt(e.finish, l.length) : null, m = Q(p ? D(f, [p]) : f);
	m && s.appendChild(m), o.appendChild(s);
	let h = H("div", "weft-step-b"), g = mt(a?.child ? a.child.doc : n?.doc);
	for (let t of g) !V(t) && t.step === e.index && h.appendChild(Pn(t, g, a?.child ? a.child.transcript : n?.transcript, i));
	if (c && h.appendChild(Rn(e.index, c, t, i)), e.reasoning) {
		let t = H("details", "weft-collapsible");
		if (i) {
			let n = `${i.scope}\u0000reasoning\u0000${e.index}`;
			t.setAttribute("data-weft-open", n), i.keys.has(n) && t.setAttribute("open", "");
		}
		t.appendChild(H("summary", void 0, "reasoning")), t.appendChild(H("div", void 0, e.reasoning)), h.appendChild(t);
	}
	e.text && h.appendChild(H("div", void 0, e.text)), e.steer && h.appendChild(H("div", "weft-note", `steered: ${e.steer.text}`));
	for (let r of e.toolCalls) h.appendChild(zn(r, e.index, t, n, i, a));
	return o.appendChild(h), o;
}
function Pn(e, t, n, r) {
	let i = V(e), a = H("div", "weft-note");
	a.setAttribute("data-weft-compaction", i ? "session" : String(e.step ?? ""));
	let o = H("div", "weft-call-h", [H("span", "weft-name", i ? gt : "compaction"), H("span", "weft-args", ht(e))]), s = Q([{ hole: "compacted" }]);
	s && o.appendChild(s), a.appendChild(o);
	let c = H("details", "weft-collapsible");
	if (r) {
		let t = `${r.scope}\u0000compaction\u0000${i ? `session\u0000${e.hash}` : `view\u0000${e.index ?? ""}`}`;
		c.setAttribute("data-weft-open", t), r.keys.has(t) && c.setAttribute("open", "");
	}
	if (c.appendChild(H("summary", void 0, "show original")), i) c.appendChild(H("div", "weft-res", _t(e)));
	else {
		let r = vt(e, n, t);
		if ("loading" in r) c.appendChild(H("div", "weft-res", "loading the transcript…"));
		else if ("gap" in r) {
			let e = Q([{
				hole: "gap",
				reason: r.gap
			}]);
			e && c.appendChild(e), c.appendChild(H("div", "weft-reason", r.gap));
		} else if (!r.messages.length) c.appendChild(H("div", "weft-res", `nothing replaced: inserted at message ${r.from}`));
		else for (let [e, t] of r.messages.entries()) c.appendChild(H("div", "weft-res", yt(t), { "data-weft-original": String(r.from + e) }));
		c.appendChild(H("div", "weft-reason", bt(e)));
	}
	return a.appendChild(c), a;
}
function Fn(e) {
	let t = -1;
	for (let n of e.steps.keys()) n > t && (t = n);
	return t;
}
function In(e) {
	let t = e === "not_recorded" ? Se : O({ hole: e }).label;
	return t.startsWith("request") ? t : `request: ${t}`;
}
function Ln(e, t, n, r) {
	let i = O({
		hole: t,
		reason: n,
		fix: r
	});
	e.appendChild(H("span", "weft-badge weft-info", In(t))), e.appendChild(H("div", "weft-reason", [i.reason, i.fix && `fix: ${i.fix}`].filter(Boolean).join(" — ")));
}
function Q(e) {
	if (!e.length) return null;
	let t = H("span", "weft-holes");
	for (let n of e) {
		let e = O(n), r = H("span", `weft-badge ${e.tone === "loss" ? "weft-warn-badge" : "weft-info"}`, e.label, { title: e.fix ? `${e.reason} — fix: ${e.fix}` : e.reason });
		r.setAttribute("data-weft-hole", n.hole), t.appendChild(r);
	}
	return t;
}
function Rn(e, t, n, r) {
	let i = H("div", "weft-req");
	if (i.setAttribute("data-weft-request", String(e)), t.badge) return Ln(i, t.badge, t.reason, t.fix), i;
	if (t.error) return i.appendChild(H("span", "weft-badge weft-err", `request could not be read: ${t.error}`)), i;
	let a = t.steps.get(e), o = a?.rows[a.rows.length - 1];
	if (!a || !o) return i.appendChild(H("span", "weft-badge", n === "running" ? `request: ${Ce}` : t.truncated ? `request: truncated — first ${(10 * Fe).toLocaleString("en-US").replace(",", " ")} requests` : "request: no record for this step")), i;
	let s = H("div", "weft-call-h", [H("span", "weft-name", "request"), H("span", "weft-args", a.rows.map((e) => `attempt ${e.attempt}`).join(" · "))]);
	a.promptChanged && s.appendChild(H("span", "weft-badge weft-info", "prompt changed at this step")), a.catalogChanged && s.appendChild(H("span", "weft-badge weft-info", "catalog changed at this step")), o.content && o.content !== "stripped" && s.appendChild(H("span", "weft-badge", o.content)), i.appendChild(s), o.content === "stripped" && Ln(i, "stripped");
	let c = o.prompt;
	if (c && !le(c)) {
		let t = H("details", "weft-collapsible");
		if (r) {
			let n = `${r.scope}\u0000request\u0000${e}`;
			t.setAttribute("data-weft-open", n), r.keys.has(n) && t.setAttribute("open", "");
		}
		let n = c.text;
		t.appendChild(H("summary", void 0, `system prompt · ${n.length} chars`)), t.appendChild(H("div", "weft-res", n)), i.appendChild(t);
	} else o.system_hash && i.appendChild(H("div", "weft-res", `system prompt ${ye(o.system_hash)}${c ? ` · ${c.badge === "stripped" ? "stripped" : In(c.badge)}` : ""}`));
	let l = o.body.tools.names;
	return i.appendChild(H("div", "weft-res", `tools: ${l.length ? l.join(", ") : "none"}`)), i.appendChild(H("div", "weft-res", `params: ${xe(o)}`)), i;
}
function zn(e, t, n, r, i, a) {
	let o = H("div", "weft-call"), s = st(e, n), c = a?.runId, l = H("div", "weft-call-h", [a?.endpoint && c ? H("a", "weft-name", e.name, {
		href: Y(a.endpoint, fn(c, {
			step: t,
			call: e.callId,
			resumed: e.resumed
		})),
		target: "_blank",
		rel: "noopener",
		title: `open this call in Studio (step ${t})`,
		"data-weft-call-link": e.callId
	}) : H("span", "weft-name", e.name), H("span", "weft-args", Gn(e))]);
	if (o.appendChild(l), e.childRunId && (r || a?.child) && (l.appendChild(a?.endpoint ? H("a", "weft-badge weft-info", "subagent", {
		href: bn(a.endpoint, e.childRunId),
		target: "_blank",
		rel: "noopener",
		title: e.childRunId,
		"data-weft-subagent-link": e.childRunId
	}) : H("span", "weft-badge weft-info", "subagent", { title: e.childRunId })), a?.child ? a.endpoint && l.appendChild(Hn(a.endpoint, e.childRunId)) : r && o.appendChild(Vn(e.childRunId, r, i, a?.endpoint))), e.result) {
		let t = Bn(r, e);
		t && l.appendChild(H("span", "weft-badge weft-info", t));
		let n = ut(String(e.result.content));
		n && l.appendChild(H("span", "weft-badge", n.kind === "bytes" ? `truncated ${n.bytes} bytes` : "not executed (max_tokens)")), e.result.isError && l.appendChild(H("span", "weft-badge weft-err", "error"));
		let i = Q(e.holes ?? []);
		i && l.appendChild(i), o.appendChild(H("div", "weft-res", e.result.content));
	} else s === "running" ? o.appendChild(H("div", "weft-res", "running…")) : o.appendChild(H("div", "weft-res weft-warn", "never completed"));
	return o;
}
function Bn(e, t) {
	if (!e?.spans) return "";
	let n = e.spans.filter((e) => e.name === "execute_tool"), r = n.find((e) => e.attrs["gen_ai.tool.call.id"] === t.callId) ?? n.find((e) => e.attrs["gen_ai.tool.call.id"] === void 0 && e.attrs["gen_ai.tool.name"] === t.name);
	if (!r) return "";
	let i = Date.parse(r.end) - Date.parse(r.start);
	return !Number.isFinite(i) || i < 0 ? "" : `${Math.round(i)}ms`;
}
function Vn(e, t, n, r) {
	let i = t.children.get(e), a = t.doc?.children.find((t) => t.id === e), o = H("details", "weft-collapsible");
	o.setAttribute("data-weft-child", e), t.expanded.has(e) && o.setAttribute("open", "");
	let s = H("summary", void 0, a ? `subagent ${a.agent || $(e)} · ${a.status} · ${ge(a.status) ? Kn(a.usage) : a.status === "running" ? _e : "—"}` : `subagent ${$(e)}`), c = a && Q(i?.doc?.holes ?? me(a));
	if (c && s.appendChild(c), o.appendChild(s), r && o.appendChild(Hn(r, e)), !i) o.appendChild(H("div", void 0, "loading the subagent's turn…"));
	else {
		let t = a?.status ?? "succeeded";
		o.appendChild(Mn(i.folded, t, void 0, void 0, n && {
			keys: n.keys,
			scope: e
		}, {
			endpoint: r,
			child: i,
			runId: e
		})), i.capped && o.appendChild(H("div", "weft-note weft-warn", "a long run: its first events are shown"));
	}
	return o;
}
function Hn(e, t) {
	return H("a", "weft-btn", "open in Studio ⤢", {
		href: bn(e, t),
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
function Un(e, t, n) {
	let r = {};
	if (t) {
		t.runId && (r.run = t.runId), n != null && n > 0 && (r.step = n), t.instructions && t.instructions !== t.registeredInstructions && (r.instructions = t.instructions);
		let e = Object.entries(t.tools).filter(([, e]) => e).map(([e]) => e);
		e.length && e.length < Object.keys(t.tools).length && (r.tools = e.join(",")), t.model && (r.model = t.model), t.thinking && (r.thinking = t.thinking), t.input && t.step === 0 && (r.input = t.input), t.engine === "scripted" && (r.engine = t.engine), t.sideEffects && t.sideEffects !== "substitute" && (r.side_effects = t.sideEffects), t.thread === "fork" && (r.thread = t.thread), t.agent && (r.agent = t.agent), t.runtimeId && (r.runtime = t.runtimeId);
	}
	return Y(e, hn(r));
}
function Wn(e) {
	return e.split("·")[0] || e;
}
function Gn(e) {
	if (e.args !== void 0) try {
		return `(${JSON.stringify(e.args)})`;
	} catch {
		return "(?)";
	}
	return e.streamedArgs ? `(${e.streamedArgs}…)` : "(…)";
}
function Kn(e) {
	let t = [`${z(e.input_tokens)}→${z(e.output_tokens)} tok`];
	return e.cached_input_tokens && t.push(`${z(e.cached_input_tokens)} cached`), e.reasoning_tokens && t.push(`${z(e.reasoning_tokens)} reasoning`), e.cache_write_tokens && t.push(`${z(e.cache_write_tokens)} cache-write`), t.join(" · ");
}
//#endregion
//#region src/panel/main.ts
function qn() {
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
function Jn() {
	let e = document.createElement("weft-devtools");
	e.autoMounted = !0, document.body?.appendChild(e);
}
function Yn() {
	if (customElements.get("weft-devtools") || customElements.define("weft-devtools", Dn), document.readyState === "loading") {
		document.addEventListener("DOMContentLoaded", () => {
			try {
				Xn();
			} catch {}
		}, { once: !0 });
		return;
	}
	Xn();
}
function Xn() {
	qn();
	let e = b();
	document.querySelector("weft-devtools") || (e.auto || oe()) && Jn();
}
try {
	Yn();
} catch {}
//#endregion
