# weft studio — the Inspector

The UI, the JSON API, the live stream and the OTLP receiver over one
[obsdb](../obsdb) database: the runs list, the run page (steps, tool
calls and results, subagents lazy, truncation badged, replay over the
event index, a time-axis waterfall when the run has spans), sessions
(threads), any trace — weft or not — a live page, and agent and tool
cards from the manifest. One `http.Handler`, no build step for users,
fully offline.

## The three setups (S4.6)

**A · embedded** — the five lines beside your app
([examples/studio-local](../examples/studio-local) is the whole thing,
with a thread session whose turns stream in live):

```go
defer otel.Install()() // local sink ./.weft/weft.db, content on, no network
mux.Handle("/studio/", http.StripPrefix("/studio",
    studio.Handler(studio.DB(otel.LocalDB())))) // same DB, same live hub
```

Passing the pipeline's handle is what makes it live: writes publish to
the handle's hub and `/api/live` follows, sub-100 ms, no network.
`studio.Open(path)` opens an obsdb sqlite file; with neither, New
opens `$WEFT_DB` or `./.weft/weft.db` (history only — a second handle
on the file, no live lane).

Without a `Token`, setup A's API (the whole `/api` tree: reads, the
live stream, the playground and debugger verbs, the runtime link)
answers only a **loopback `Host`** — `localhost`, `*.localhost`,
`127.0.0.0/8`, `[::1]`, any port — so a DNS-rebinding page
(`http://evil.example:7331` resolving to 127.0.0.1, same-origin to the
browser) gets a 403 instead of your transcripts and your playground.
An app served on a real hostname (`http://myapp.internal:8080/studio/`)
either lists that origin — `studio.AllowOrigins("http://myapp.internal:8080")`,
whose `host:port` must equal the request's `Host` — or sets
`studio.Token`. `X-Forwarded-Host`/`Forwarded` are never trusted; behind
a proxy, the `Host` the proxy forwards is the one checked. The UI shell
and `/panel.js` carry no data and are not checked; with a `Token` the
bearer is the defence and the `Host` does not matter.

**B · local binary** — any language's app, several services:

```sh
go install github.com/weftgo/weft/cmd/weft@latest
weft studio                            # UI + OTLP + SQLite + playground + dev token on 127.0.0.1:7331
./my-go-app                            # otel.Install() finds it through the discovery file
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:7331 python app.py
```

The dev token is stable per database — `<db>.token` beside the SQLite
file, 0600, never printed (`--rotate-token` renews it;
`WEFT_STUDIO_TOKEN` or `--token` overrides it); a Go app with
`otel.Install()` / `runtime.Install()` finds the running Studio through
the discovery file `studio.json` (`./.weft`, `$XDG_RUNTIME_DIR/weft`,
the user cache directory; `WEFT_STUDIO_URL` always wins,
`WEFT_DISCOVERY=off` ignores it); ingest is open on loopback;
`127.0.0.1:7331` is the one default port: started again on the same
database, the binary finds the running Studio through `/api/meta`
(`db.path`, `pid`, read with the stable token — sent only to an
address a trusted discovery file names, so from another directory with
`--db` on the same file the probe goes out bare and a second Studio
starts) and reuses it (`studio
already running at … (pid n), reusing`, exit 0); anything else on the port
moves it to the next free one in 7331–7340 with one line saying so.
`--addr` (or `WEFT_STUDIO_ADDR`) pins: a busy pinned address is an
error, never a fallback; `--db sqlite://path` picks the
file, `--db clickhouse://user:pass@host:9000/db` the hosted backend
(`obsdb/clickhouse`, the one place the driver is imported). The
playground is on (`studio.Playground(true)`), inert until an app's
`weft/runtime` connects; `--no-playground` turns it off. The manifest
is `--manifest` (`WEFT_MANIFEST`), else the nearest `weft.json` from
the working directory upward — one line says which file, or that none
was found. `--open` (default on when stdout is a terminal) opens the
UI with the token in the URL fragment.

The same binary is the API over a terminal, each subcommand one
route, `--url` (`WEFT_STUDIO_URL`) and `--token` (`WEFT_STUDIO_TOKEN`)
picking the Studio: `weft runs [--agent] [--since] [--failed] [--limit]
[--json]` (`GET /api/runs`; a limit that hid runs says so on stderr),
`weft open <run id> [--open] [--with-token]` (checks `GET
/api/runs/{id}`, prints the bare `<url>/runs/<id>`; the token goes only
to the browser, or to stdout on `--with-token`), `weft export <run id>
[--format json|jsonl|otlp]` and `weft export <run id> --wefttest
./testdata [--test TestName] [--force]` (`GET /api/runs/{id}/export`,
the fixtures unzipped where `wefttest.Replay(t, "testdata")` reads
them; `--force` replaces a non-empty target's fixtures), `weft doctor`
(`GET /api/meta`).

**C · hosted** — the same handler behind `studio.Token`: the panel's
scoped tokens are HMAC-signed `{public_id, scope, exp}` minted by your
backend through `POST /api/panel-tokens`; every data route refuses
anything outside the token's public id. A token is read-only unless
minted with `"playground": true` — a read-scoped one neither acts nor
reads the agents' system prompts (`/api/manifest`, the runtimes'
instructions) — and experiments (`experiment_id` included) are the
server token's alone.

## The devtools panel (WEFT-DEVTOOLS.md)

One script tag puts the run loop in the corner of your own page —
`/panel.js` is served by the same handler:

    <script type="module" src="/studio/panel.js" data-weft data-scope="pub_…"></script>

Rung 1 is a viewer scoped to that conversation: the turns (parked
shown), the step story with tool calls, usage splits, each step's
attempts and timing ("attempt 4 of 4 · fallback to glm-b", "1.2 s ·
ttft 180 ms"), approvals
read-only, truncation/gap/stripped honesty, the raw JSON, a live tail,
⤢ deep links into Studio, lazy subagents (one level inline, with the
child's request line and an "open in Studio" hand-off; a grandchild is
the hand-off only) and a spans waterfall.
Deep links (plan G1) follow one scheme, `src/lib/links.ts`, shared by
the panel and Studio's app (an ESLint rule refuses a Studio URL built
anywhere else): `runs/<id>?step=<n>&view=story|raw&sel=…&axis=time&t=…`,
`sessions/<id>`, `traces/<id>?span=<span id>`, `playground#run=…` (the
hand-off in the fragment) and `playground?experiment=<id>`. `step` is
the step's ordinal — its index as the loop counts it, the `n` of
`runs/{id}/steps/{n}` — never an event position or a transcript batch
index; a call is named with its step (`sel=c:<step>:<call id>`,
`c:resume:<call id>`). ⤢ carries the step being read (else the running
one); each tool call, subagent badge, session and trace in the panel is
a link. No link carries a token, and no request does in its URL: the
token travels in the `Authorization` header, and each live stream is
opened with a grant (`POST /api/live-grant`, the bearer in the header;
the stream URL carries only the `sig`). The browser's own reconnect is
left to resume with `Last-Event-ID` while the grant lasts; one after
its 60 s, or one Studio refused (a restarted Studio's new grant key),
gets one new grant and a fresh connection (the panel refetches what it
lists). A panel token's stream that ends with `event: expired` is not
asked again: the live dot goes out, history stays, nothing reaches the
console — a new token is a new `data-token`, and the panel restarts
its connection on it.
`Alt+W` toggles (Q4); the other keys, the dock's layout and what it
remembers are under "Layout" below. Setups B/C add
`data-endpoint` and `data-token` (a dev token, or a panel token your
backend mints per page via `POST /api/panel-tokens`). A single-page app
that switches conversations sets `window.__WEFT__ = { scope }` (or
`{ publicId }`) instead of `data-scope`; the tag then carries no other
knob, so keep `data-weft` on it — it is how the panel finds its own tag
(and with it the endpoint) whatever the file is called.

The panel follows a scope, `pub_…;session=s_…;flow=f_…;run=r_…`
(`src/lib/scope.ts`; only the public id is required). The public id
selects the conversation; `run` pins the selected turn, and its live
tail, to that run once (your clicks own the selection afterwards), or
says `run r_… not in this conversation` when it is not among the
conversation's runs; `session` narrows the turn list to that session's
runs (a run without a session id is not judged, and a list whose runs
carry none says it is not narrowed); `flow` shows as a chip in the
header and filters nothing yet (it waits for `weft/flow`).
`data-public-id="pub_…"` is still read, as the scope `pub_…`, but it
is deprecated: `data-scope` is the form.

Where the scope comes from (plan C3, scope detection). Each rung after
the first is passive, and `data-detect="off"` turns rungs 2 and 3 off;
rungs 1 and 4 always work — no `data-detect` value and no token turns
them off. The order is explicit (rung 1) > the URL (rung 4) > a marker
(rung 3) > a header (rung 2): the numbers are the order the rungs were
added, the precedence is how deliberate each one is — the host's own
word, then a hand-off a link made (Studio's dev links), then what the
page shows passively. The footer says which is in effect in one word:
the rungs installed — `detect: headers` (with `(chained)` when the
`fetch` it wrapped was not the browser's own), `detect: markers` or
`detect: headers+markers` — led by `url` (`detect: url`,
`detect: url+markers`, …) while the scope followed is the URL's, else
`detect: off`, `detect: explicit` (rung 1 names the scope, nothing
detects) or `detect: none` (nothing names one, and rungs 2 and 3 are
off by default here).

| # | Rung | When it is on | The explicit alternative |
|---|---|---|---|
| 1 | explicit: `data-scope` (element, `weft:scope` meta, script tag), `window.__WEFT__ = { scope }` or `{ publicId }`, `scope()` / `mount({scope})` from `@weftgo/devtools`, the deprecated `data-public-id` | always; it wins over every detected scope | — |
| 2 | response headers: the `Weft-Scope` header of the page's own same-origin `fetch` responses (the scope header below) | by default only with the page and the endpoint on loopback (`localhost`, `*.localhost`, `127.0.0.0/8`, `[::1]`) and no token or a dev/server token, and only once Studio has answered; anywhere with `data-detect="headers"` (or `mount({detect: "headers"})`); never under a panel token (`weft_pt.`) unless asked | `data-scope` |
| 3 | the DOM marker: `data-weft-scope="pub_…;session=…;flow=…;run=…"` on any element of the page — the framework helpers (`useWeftDevtools` for React and Vue, the Svelte `weftDevtools` action) set it on the element they are given | by default everywhere except a page off loopback under a read-scoped panel token (a playground-scoped panel token, the dev token, no token, or any token on loopback keep it on); anywhere with `data-detect="markers"` (or `"headers,markers"`, `mount({detect: "markers"})`) | `data-scope` |
| 4 | the page URL: `?weft_scope=…` (read first), else `#weft_scope=…` — the same string form, URL-decoded (`#weft_scope=pub_x%3Bflow%3Df_1`); a value without a public id is ignored | always, every setup and token, `data-detect="off"` included; below rung 1, above rungs 2 and 3; re-read on `hashchange` and `popstate` | `data-scope` |
| 5 | the fallback: no rung names a scope — the header reads "no conversation detected on this page · how to scope" and lists the newest runs | when 1–4 name nothing | any line under "how to scope": `data-scope="pub_…" on the panel's <script> tag (or <weft-devtools>)`, `scope("pub_…") from @weftgo/devtools`, `data-weft-scope="pub_…" on the chat's element`, `scope.Header(h, …) on the app's handler (Go, package weft/scope)` |

Rung 2 patches a global of your page, `window.fetch` (shadow DOM
scopes DOM and CSS, not JavaScript), so it is held to these rules
(`src/panel/detect.ts`): it reads the response URL and the
`Weft-Scope` header only, never a body, never a clone, and never sends
anything; it reads a same-origin response, or a cross-origin one only
when the page and the response are both on loopback (a dev server on
`:5173` calling the app on `:8080`) — any other cross-origin response
is ignored even when it exposes the header, so a cross-origin
production app uses `data-scope` or the DOM marker; the panel's own
Studio requests are never read; it is installed only once Studio has
answered, so a panel whose Studio never answers never touches `fetch`;
it chains to the `fetch` it found (the browser's own or
another library's patch) and returns the same response or rejection;
it is put back exactly as found when the panel disconnects or the rung
is turned off, unless another patcher wrapped it after the panel (then
that stack is left alone, the wrapper goes inert and the footer says
`fetch not restored`) — so with two panels on one page, the first to
disconnect leaves its inert wrapper in the chain, a pass-through; and
nothing it does reaches the console. It is
fetch-only: a page cannot read an `EventSource`'s response headers, so
an SSE chat app names its scope with rung 1 or rung 3.
WebSocket is never wrapped. A detected scope of the conversation the
panel follows (the same public id, session and flow — the next turn's
header) is a narrowing, never a restart: its run is pinned unless you
have clicked a turn since the last pin. Another conversation is
followed only from the request path that set the current one, so two
widgets polling for different conversations do not thrash; the panel
keeps the newest scope per path and one entry per conversation seen
(with its newest run), for the switcher below. A run the scope names
that is not listed yet (a streaming handler's header lands before its
run row) is looked for once more a second later before the panel says
it is not in the conversation.

Rung 3, the DOM marker, is the production rung: it touches no global
of your page (`window.fetch` stays as you left it) — it reads
attributes only, through one `MutationObserver` on
`document.documentElement` (only changes that add, remove or re-mark a
marked element count; rescans debounced ~100 ms, trailing, but at most
500 ms apart while such changes keep coming, and none while the page is
hidden — one when it is shown), one passive, capturing `focusin`
listener and one `visibilitychange` listener on `document` (event
listeners, not patches), all removed when the panel disconnects or the
rung is turned off (`src/panel/markers.ts`). It never writes to your
DOM, never reads the panel's own tree (a `<weft-devtools>` and
anything inside it are skipped, and focus inside the panel — even a
panel mounted inside a marked chat — is never a chat's), ignores an
empty value or one without a public id, and prints nothing; a scan
that changes nothing draws nothing. The marker must sit in the light
DOM: one inside a widget's own shadow root is invisible to the scan
(and to focus's `closest()`) — put it on the host element at or above
the widget. With several markers on one page the
one nearest focus wins: the closest marker around the focused element,
else the one focus was last in, else the one followed now, else the
first in document order. A marker's scope is a conversation (public id,
session, flow) whose run narrows it, so following it is C3.2's rescope
— the same conversation narrows, another restarts, a turn you clicked
stays. A followed marker that leaves the page stays followed — only
focus or the switcher moves the panel — except on a route change: when
every old marker is gone and exactly one new one appeared in the same
scan, the new one is followed. An explicit `data-scope` (or
`window.__WEFT__`) wins over a marker, unless a marker carries the
same conversation — the helpers call `scope()` and set the marker, so
focus moves between the helpers' chats; from then until the explicit
scope changes it counts as a marker's, even after that marker leaves,
and its run (a helper's next `scope()`) pins as an explicit run does.

The scope switcher: when more than one conversation is known (or one,
while the panel follows a conversation no longer on the page) — the
explicit scope, then the markers in document order, then the
header-detected scopes in first-seen order, each conversation once
under the first source that names it, at most 20 — the panel header
shows a `<select aria-label="conversation">` listing each by its public
id (with its session and flow when set) and its source (`explicit`,
`marker`, `header`); `●` (live) or `○` marks the one followed.
Choosing one follows it and pins its run (a forced rescope) until focus
next goes into a marker or the explicit scope changes. A followed
conversation that is no longer known is shown as "not on the page",
not offered. One known conversation shows no switcher.

| `data-detect` | rung 2 (headers) | rung 3 (markers) |
|---|---|---|
| unset | page and endpoint on loopback, any token but a panel token (`weft_pt.`) | on, except a read-scoped panel token off loopback |
| `headers` | on anywhere | as unset |
| `markers` | as unset | on anywhere |
| `headers,markers` | on anywhere | on anywhere |
| `off` (named anywhere: `off,headers` is `off`) | off | off (rungs 1 and 4 stay on) |

Empty words are skipped (`markers,` is `markers`); a value naming
anything else (`bogus`, `headers,bogus`) is unset: the defaults apply.

Rung 4, the URL, reads `location` and listens to `hashchange` and
`popstate` — two passive listeners on `window`, removed when the panel
disconnects (event listeners, not patches); it never writes the URL
(no `pushState`, `replaceState` or hash change, whatever the switcher
does). A new URL scope drops a switcher choice and pins its run, as a
new explicit scope does; an explicit scope set later wins over it, and
removing that hands the panel back to the URL. Focus in a marked chat
does not move the panel off the URL's scope; the switcher (which lists
it as `url`, after `explicit`) does. The parameter is read from the
query and from the fragment, a hash router's own query included
(`#/chat?weft_scope=pub_…`); a value ends at the next `&` or `?`, is
decoded once (a value encoded twice, whose public id still holds `;` or
`=`, is ignored), and one without a public id is ignored. Because the
rung listens instead of patching, a navigation the page makes with
`pushState` or `replaceState` is not seen until the next `hashchange`
or `popstate`, or an attribute change on the panel.

Rung 5, the fallback: with no scope from any rung, the header says
`no conversation detected on this page · how to scope`, and "how to
scope" opens the four one-line fixes above. The newest runs it lists
follow `/api/live?agent=<agent of the newest listed run>` (run frames
only, opened through a grant like every stream) instead of a poll: the
live API has no selector for everything (exactly one of `run`,
`session`, `public_id`, `agent`), and an app's dev page usually runs
one agent. While that stream is up the list says `live: agent X · the
other agents' runs every 30 s` and is still read every 30 s (dock open,
page visible), so another agent's runs that start later appear. The
10 s poll (dock open, page visible) takes over while no stream is up —
no run listed yet (no agent to name), the stream gone (it is reopened
on the bounded backoff — 5 s, doubling, capped at a minute, five times at most; the poll does
not reopen it in between), or a grant refused (`streaming needs the
server token · polling`; a panel token is never granted an agent's
stream, so under one the panel never asks and says the same). In the
fallback only the streamed agent's runs light the pill below.

The collapsed pill is an activity signal: while the stream the panel
holds anyway (its scope's `public_id` stream, or the fallback's agent
stream — no second one is opened) lists a run as running, the pill
pulses (a CSS animation, none under `prefers-reduced-motion`) and shows
the run's live step count, `● 3`, with `aria-label` and title
`weft devtools · running, step 3`. The count is the open tail's steps
when that run is the turn the panel follows, else its row's (unknown
while it runs: `●` alone, `weft devtools · running`). When the run
ends the next draw is the plain pill.

Layout (plan D1). The dock floats — dragged by its header, resized
from its corner, between 360×280 and the viewport minus 16 px, clamped
again on every window resize (a passive listener, removed on
disconnect) — or docks to the `left`, `right`, `bottom` or `top` edge,
resized along that edge (pointer events with pointer capture, no
library; touch works through them). Collapsed it is the pill: the live
dot and step count above, and the current turn's cost — its tokens
in→out once the run has finished, `—` while it runs (Studio prices no
usage, so tokens are the cost). Hidden draws nothing: the element, its
API and its events stay, and `Alt+W` or `open()` bring the dock back.
Under 640 px of panel width the turn column is a dropdown and the
header's actions wrap — the default 520 px float is that narrow, so the
dropdown is what it shows until you widen it; under a 480 px viewport
the panel is a full-width bottom sheet, 70 vh at most. The resize
handles are pointer-only (`aria-hidden`; a keyboard resize is D3's). The header's `⇆` button and
`Alt+Shift+W` go to the next layout: float, dock right, bottom, left,
top.

| Attribute | What it sets | Default |
|---|---|---|
| `data-position` | the initial place: `bottom-right` or `bottom-left` (the float's corner), `right-dock`, `left-dock`, `top-dock`, `bottom-dock`; set later, it moves the dock | `bottom-right` |
| `data-open` | start expanded | collapsed (the pill) |
| `data-mode` | the initial mode: `float`, `dock`, `pill` or `hidden` | from the two above |
| `data-push="true"` | while docked and open, pads `<html>` on the docked side by the dock's size — `padding-<side>: var(--weft-devtools-inset)`, the variable set to the size (the bottom sheet: `padding-bottom` by its `70vh`) — so the page's flow never sits under the panel. It replaces the inline padding on that side while docked; the inline values from before (or one your page set meanwhile) are put back exactly on close, on a mode change and on disconnect. The panel's one write to your document, opt-in. Padding does not move `position: fixed` or `sticky` elements: a fixed or sticky composer keeps clear with the variable in your own CSS, `bottom: var(--weft-devtools-inset, 0)` (and the matching `right`/`left`/`top` for those docks) — push sets the variable as well as the padding | off |
| `data-z-index` | the dock's and the pill's z-index (an integer) | `--weft-z` on the element, else 2147483000 |
| `data-theme` | `light` or `dark` names the theme (above everything); `auto` leaves it to the order below | `auto` |

Theme (plan D2). The panel is light or dark, resolved in one order:
(1) explicit — `data-theme="light|dark"` on the element, `weft:theme`,
the script tag, or `mount({theme})`; (2) stored — the header's `◐`
button cycles auto → light → dark (disabled, and saying so, under an
explicit theme), kept as `theme` in the key below, and auto clears it;
(3) auto — your page's `<html data-theme="dark|light">`, else
`<html class="dark|light">`, else `<html>`'s computed `color-scheme`
when it is exactly `dark` or `light`, then the system's
`prefers-color-scheme`, else dark. A theme toggle on your page is
followed (one passive attributes-only observer on `<html>`'s `class`
and `data-theme`, and one media-query listener, both removed on
disconnect); `<html>` and `matchMedia` are only read. The result is
`data-theme-resolved` on the `<weft-devtools>` element itself. The
palette is the Studio app's (`src/lib/palette.ts`, which names the
`src/styles.css` variable each shared token equals — a test fails on
drift), and both themes clear WCAG AA (`theme.test.ts`: 4.5:1 text,
3:1 UI). Every colour, radius and font is a custom property on the
panel's `:host`, so your CSS overrides any of them — an outer rule
beats the shadow tree's `:host`, in either theme:
`weft-devtools { --weft-bg: #fff }`. The tokens: `--weft-bg` (panes),
`--weft-bg2` (header, steps, footer), `--weft-bg3` (selected row,
fields), `--weft-fg`, `--weft-dim`, `--weft-faint`, `--weft-line`,
`--weft-accent`, `--weft-on-accent` (text on the accent), `--weft-warn`,
`--weft-err`, `--weft-info`, `--weft-ok`, `--weft-parked`,
`--weft-scrim` (the `?` overlay), `--weft-shadow`, `--weft-font`,
`--weft-radius`, `--weft-radius-sm` (and `--weft-z`, above).

The panel remembers, per origin, in `localStorage["weft.devtools"]`
— one JSON object, `{v: 1, mode, side, open, hidden, x, y, w, h, d,
run, theme, raw, debug}`: the mode (`float`/`dock`), the docked side,
open, hidden, the float's box (`x`, `y`, `w`, `h`, px), the dock's size
(`d`), the last selected turn (`run`, by run id), `theme` (the `◐`
choice: `light`, `dark`, or `""` for auto), the raw view, and `debug` (the old `localStorage.weft_debug=1`
switch, migrated into the key and dropped). The placement fields are
written only once you place the panel (toggle, drag, resize, re-dock):
until then the `data-*` attributes above decide on every load; after,
the stored placement wins over them. Another version is ignored; every
read and write is guarded, so a private window just forgets.
`localStorage.removeItem("weft.devtools")` resets everything (and turns
`debug` off).

The keyboard (`src/panel/element.ts`'s `SHORTCUTS`, the `?` list).
`Alt+W` is the one key the panel hears on `window`; every other key is
heard on its shadow root, so it fires only while focus is inside the
panel — a key typed into your page never reaches it, and keys typed
into the panel's own fields are typing. `Alt+W` that opens the dock
puts focus in it, so the keys below work at once.

| Key | Does |
|---|---|
| `Alt+W` | toggle the dock from the page — not while a text field has focus (`Ctrl+Shift+W` too, where the browser delivers it) |
| `Alt+Shift+W` | next layout: float, dock right, bottom, left, top |
| `Esc` | close (the `?` list first); it is not stopped, so your page's own `document` Esc handlers (a modal) still run — the panel never blocks them |
| `j` / `k` | next / previous turn |
| `J` / `K` | next / previous step (⤢ carries it) |
| `g s` | open the turn and step in Studio (`lib/links.ts`, the link ⤢ carries), in a new tab |
| `r` | raw JSON of the open turn |
| `/` | search — reserved for D4, does nothing yet |
| `?` | the key list |

Where the configuration comes from (plan C2). Each field — `endpoint`,
`scope` (or the deprecated `public-id`; `scope` wins at the same rung),
`token`, `detect`, `position`, `open`, `auto`, `global`, `mode`, `push`,
`z-index`, `theme` — resolves on its
own; the first source that sets it wins, so a meta tag can carry the
token while the script tag carries the endpoint. The panel never reads
its own file name: a bundle served as `/assets/devtools.abc123.js`
behind a proxy configures itself the same way.

| # | Source | The explicit form |
|---|---|---|
| 1 | `mount(opts)` — a programmatic mount (`import { mount } from "@weftgo/devtools"`, the npm entry; not in the script-tag bundle) | `mount({endpoint, scope, publicId, token, detect, position, open, auto, theme, target})` |
| 2 | the `<weft-devtools>` element's attributes | `<weft-devtools data-endpoint="…" data-token="…">` |
| 3 | meta tags | `<meta name="weft:endpoint" content="…">` (also `weft:scope`, `weft:public-id`, `weft:token`, `weft:detect`, `weft:position`, `weft:open`, `weft:auto`, `weft:global`, `weft:mode`, `weft:push`, `weft:z-index`, `weft:theme`) |
| 4 | the panel's `<script>` tag: the running classic script, else the first with a `data-weft` attribute (any `src`, any value), else the first carrying one of the `data-*` attributes above (`data-mode`, `data-push`, `data-z-index` and `data-theme` excepted: other scripts use those words) | `<script type="module" src="…" data-weft data-endpoint="…">` |
| 5 | `panel-config.json` beside the script (`/studio/panel.js` → `/studio/panel-config.json`), asked only when rungs 1–4 named no endpoint; its `endpoint` is taken on the script's own origin only, and it never carries a token | name the endpoint at any rung above |
| 6 | the script's own origin + directory (setup A) | name the endpoint at any rung above |

A renamed bundle whose tag carries none of the panel's `data-*`
attributes needs `data-weft` on it (or a higher rung); without a tag to
find, the endpoint is the page's own directory.

A `weft:token` meta tag, like `data-token`, is a token in the page's
source: in HTML you ship, use a per-page panel token your backend
mints (`POST /api/panel-tokens`), never a dev or server token.

No Studio answering: the dock the script mounted by itself removes
itself silently — no console, at most two requests (`panel-config.json`,
then meta; `panel-config.json` gives up after 3 s). A mount you made (a
`<weft-devtools>` element in your markup, `mount(opts)`, or
`data-auto="false"`) shows one quiet line instead, `Studio not
reachable at <endpoint> · retry`, where `retry` asks again (the line
reads `checking…` while it does). The
artifact is built by
`studio/web/vite.panel.config.ts` (a separate library-mode build), the
committed `studio/dist/panel/panel.js`, 162,007 B raw / 45,836 B gzip
(44.8 KiB, under the 80 KiB cap). The scope-detection ladder (C3: rungs
2 to 5 and the activity pill) cost +6.0 KiB gzip against its +3 KiB
estimate: nothing deferrable supplies the first scope and the
deferrable remainder is under 1 KiB, so the overrun is accepted rather
than split. The host API below cost about +3.1 KiB against +1 KiB (with its
review fixes); the
layout (D1) +3.6 KiB against +4 KiB (with its review fixes).
`make studio-panel-asset` stages it as `panel-<version>.js` + sha256
for non-Go backends.

The size ledger. Every panel build (`bun run build`, `make
studio-build`, `make studio-check`) prints the per-item size table:
each plan item's estimate beside its measured gzip delta, `over` when
the delta is more than half again the estimate (`over (accepted)` when
the plan recorded why it was not split), the unbudgeted items, the
baseline, and the total against the 80 KiB cap with the headroom left.
The deltas come from `studio/web/panel-budget.json`, a committed,
append-only ledger: one row per landed item — `{"label": "D1", "item":
"D1", "gzip": <panel.js's gzip bytes after it>}`, `item` the budget
line it counts against (`""` for none) — and earlier rows are never
edited. An item appends its row in the same commit as its rebuilt
`studio/dist/panel/panel.js`: the build says when the built file
differs from the last row, and `src/panel/budget.test.ts` — run by
`make studio-check` — fails until the last row is the committed file,
so a rebuild that forgets its row fails studio-check. The per-item
marks are evidence, not a gate; the build itself fails only over the
cap (`vite.panel.config.ts`).

The same file is on npm as `@weftgo/devtools`, for apps that bundle
everything and ship no `<script>` tag (Vite, Next, SvelteKit). This
is a second delivery, not a replacement: the handler above is still the
canonical install. `import "@weftgo/devtools"` loads the package's
`panel.js`, which has the same sha256 as `/studio/panel.js` of the same
version, and so does what the tag does. Beside it are a thin typed
module (`mount`, `scope`, `open`, `close`, `toggle`, `on`,
`serializeScope`/`parseScope`, and the host API below: `select`,
`isOpen`, `studioLink`) and the `/react`, `/vue` and `/svelte`
helpers, which set the `data-weft-scope` marker and are not components.
The package has zero runtime dependencies and its version is the weft
version. `studio/web/npm/README.md` has the API table. `make devtools-npm` assembles the package in
`studio/web/npm`, runs the package suite against the assembled files
and lists the tarball. `make studio-check` fails if the package's
`panel.js` is not the served one. Publishing is done by hand. A Vite app
that installs the packed package is in `examples/devtools-vite`
(`make devtools-vite-check`).

The host API (plan C4) drives the panel from the page's own UI — a
"debug this" button beside a reply, a "report this run" link — with no
Studio URL in the page. It is the `<weft-devtools>` element's own
methods, one implementation for both installs: under the script tag the
bundle publishes them as `window.weft.devtools`, the one global the
panel adds (below); from npm the package's exports call them on every
panel on the page.

| Method | What it does |
|---|---|
| `open()`, `close()`, `toggle()`, `isOpen` | expand, collapse or flip the dock; `isOpen` reads it (from npm, `isOpen()`) |
| `scope(s)` | the explicit scope (rung 1: it wins over the URL, markers and headers, and its run pins): a `Scope` or its string form (`"pub_…;session=…;run=…"`); `sessionId`/`runId` are read as `session`/`run`, so a `run` event's detail can be fed back; one naming neither a public id nor a session reads `scope: no public id or session`. `scope(null)` clears what `scope()` set and puts back what `mount()` named, so the ladder decides again (a `data-scope` in your markup stays yours) |
| `select(runId, step?)` | selects that turn, and the step by its ordinal (G1), once a `scope()` just before it has settled; ⤢ then carries the step. A run the list does not show is read by id (`GET /api/runs/{id}`, scoped by the token) and joins the list when it is the conversation's (a run with no session id — a playground run — is not judged by a session narrowing, as in the list); else the line `run r_… not in this conversation` (`run r_…: Studio did not answer` when the read failed). A step the run does not have reads `step n not in run r_… · showing step m`, and the last step is carried. A call made while the panel's start hangs waits 10 s, then reads `select r_…: the panel is not connected` |
| `on(event, cb)` → unsubscribe | follows one event (below); a `cb` that throws is swallowed |
| `studioLink(runId, step?)` | the Studio page of that run (and step) through `lib/links.ts` — the link ⤢ carries, never with a token; `""` before the panel knows its endpoint |

A scope naming a session and no public id (`scope({publicId: "",
session: "s_…"})`, or `";session=s_…"`) is resolved through `GET
/api/sessions/{id}/public_id`, which only setup A and the dev token may
ask: under a panel token the panel does not ask, and says `session s_…:
the session lookup needs the dev token` (a 403 reads the same); a
session created without `thread.PublicID` reads `session s_… has no
public id · not recorded …`, an unknown one `… · unknown session`, a
lookup Studio did not answer `session s_…: Studio did not answer the
lookup`. The panel keeps its scope and nothing is thrown.

The events are `CustomEvent`s named `weft:run`, `weft:parked` and
`weft:error`, dispatched from the element (`bubbles`, `composed`) after
the panel's own work (a microtask), so `document.addEventListener`
hears them too. Only runs of the conversation followed are reported
(none in the fallback), and only transitions the panel sees: a
conversation's first read of its list is history — only a run reading
running there, and the run the scope pins, are reported.

| Event | `detail` | When |
|---|---|---|
| `run` | `{runId, status, publicId?, sessionId?, step?}` — `status` the turn list's word (`running`, `succeeded`, `failed`, `parked`, `interrupted`), `step` the run's last step ordinal the panel knows | a run starts, and each status change, once per transition |
| `parked` | `{runId, callId, ackId, name}` — `ackId` is the id the approval names, the call id `weft.Approve`/`Deny`/`Resolve` and `POST /api/runs/{runId}/approvals` (`call_id`) take, so it equals `callId`. The app approves its own turns through its Session, `s.Decide(ctx, thread.Approve(ackId))`; the Studio route is for playground runs only (403 for a run no runtime started) | once per call a run parked on (read from its `run_finish`'s pending list) |
| `error` | `{message, runId}` | a run of the conversation failed: its error, once per run — run errors only (the panel's own trouble, Studio not answering, is a line in the panel) |

The global: while a `<weft-devtools>` from the script tag is connected,
`window.weft.devtools` is its API object (`window.weft` is created only
when absent — an existing plain object gets the one property, anything
else is left alone and the footer says `global: window.weft is the
page's`; a `devtools` of your own is never replaced). It is deleted on
disconnect (with `window.weft` when the panel created it and nothing
else is in it). `data-global="off"` on the tag or element (or `<meta
name="weft:global" content="off">`) keeps it off; the npm package adds
none. `examples/studio-local`'s page uses it: "debug this" per reply,
"report this run" from `on("run")` and `studioLink`, and `on("parked")`
counted into `<body data-parked-count>` (ask it about a refund: the
turn parks on `refund_order`, a `weft.RequireApproval` tool).

The scope header (plan C3, rung 2, the development rung) is set by the
app's own handler, since `thread` has no HTTP layer: one line,
`mux.Handle("POST /chat", scope.Header(chat, func(r *http.Request)
scope.Scope { return scope.Scope{PublicID: pubOf(r)} }))` (package
`github.com/weftgo/weft/scope`), sets `Weft-Scope: pub_…` on every
response, and a handler that knows the run once its turn starts calls
`scope.Set(w, scope.Scope{PublicID: …, RunID: turn.RunID()})` before
writing (`examples/studio-local`'s `/run` answers `Weft-Scope:
pub_demo;run=<id>`). It never carries a token. The value is the
`data-weft-scope` marker's string form, pinned for both sides by
`studio/testdata/scope.golden.json`. Both helpers append `Weft-Scope`
to `Access-Control-Expose-Headers`; a page on another origin also
needs your CORS policy to allow its origin. Put `scope.Header` inside
your CORS middleware, or list `Weft-Scope` in its exposed headers. The panel reads the header
through rung 2 of the scope-detection ladder above (on by default with the page and the endpoint on
loopback and no or a dev token, `data-detect="headers"` elsewhere;
same-origin responses, or cross-origin ones between two loopback
origins) — `examples/studio-local`'s tag names no
scope, and its first `/run` scopes the panel to `pub_demo` and pins
that run.

## The playground (WEFT-PLAYGROUND.md)

Your app dials Studio out and executes experiment commands as runs of
the agents it registers — setup A is five lines
([runtime/examples/local](../runtime/examples/local) is the runnable
demo behind the P0 curl gate):

```go
defer runtime.Install(runtime.Local(srv), // or runtime.Studio(url, tok)
    runtime.Agents(support), runtime.Limits(runtime.Budget{MaxTokensPerExperiment: 200_000}),
    runtime.AllowSideEffects("send_email"))() // a write tool; a read is weft.Replay(weft.ReplaySafe)
```

Studio's side is `studio.New(..., studio.Playground(true))`, which
serves `GET /api/runtimes` (the connected runtimes),
`POST /api/playground/runs` (the §10.4 validation table: 400 unknown
tool/model names and unsupported 8b modes, 403 raised limits or a
refused side-effect tool, 404 unknown runtime/agent/source run, 409 a
reused command id, 503 with no runtime connected) and
`GET /api/playground/commands/{id}`, plus the runtime link's own
routes (`POST /api/runtime/register`, `GET /api/runtime/commands` SSE,
`POST /api/runtime/acks`). Safety: off unless `WEFT_ENV=dev` or
`runtime.Enabled(true)`; overrides only narrow; a side-effect tool's
call is substituted with its recorded result or parked
(`weft.Replay(weft.ReplaySafe)` vouches a read, `AllowSideEffects`
opts a tool into allow mode); budgets cap each experiment; the app's
own runs are never touched.

P1–P5 ride the same command: `transcript_edits` (validated on both
sides — a patch names a call in the kept prefix, the prefix ends at a
step boundary with every call answered), `engine: scripted` (the
source run's recorded turns at zero tokens; scripted + an
instructions/model override is refused — the §5.5 prompt trap), thread
`fork` (a new session with lineage the panel can keep chatting in),
`POST /api/runs/{id}/approvals` (a parked run's continue/skip/resolve
— ADR 0007's own verbs), `POST /api/playground/fixtures` (the run's
records as wefttest replay fixtures; refused to a read-scoped panel token, 403 with badge `hidden`; 409 with its badge when there is nothing to fixture), and `GET/POST /api/experiments`
with `GET /api/experiments/{id}` (the saved groups, PQ4). The
debugger's rungs 3–4 act on runtime-started runs only (D7, PQ7):
`PUT /api/runtimes/{id}/breakpoints` (capability `breakpoints`) and
`POST /api/runs/{id}/steer` (capability `steer`) — `meta.debug_scope`
says so. The panel's experiment drawer (the §3 form, the live result
with the inline diff, the approvals, the 2-way compare) and the
Studio `/playground` page (variants side by side with the metrics, the
E9 variants × inputs matrix, the experiment history) both render them,
each control only for its reported capability.

The runs list (light): status as a dot and a word, the error under a
failed run's id, session/public-id/experiment filters that mirror the
URL, the session column linking each turn to its thread, and a follow
toggle that streams an agent's runs as they happen:

![runs list, light theme](screenshots/runs-light.png)

The run page (light) — the prompt and the answer first (the answer now
comes from the transcript: deltas are live-only), then the facts; the
**trace** draws on the **time axis** when the run has spans (real
durations, `?axis=` picks; the position axis keeps the replay
playhead), and subagent children sit as nested rows under the step
that called them (agent, status, usage, holes, a link) and expand
lazily — their events and their request record (the child's prompt,
read by the child's id) are their own runs', fetched on demand. Each
step card's **Request** pane shows what the step called the model with:
the system prompt (diffed against the previous step, or at the first
step against the registered instructions when they differ; never over
a cut record, and "too large to diff" past 2,000 differing lines a
side), the tool catalog (description, schema, policy chips), every
param ("adapter default" when nil), tool choice, thinking, the attempts
and the messages sent (count and bytes computed from the transcript as
JSON, the last three inline, the rest as a raw tree), with the chips the
hashes decide — **changed by PrepareStep** (`system_hash` moved with
the tool set unchanged, or at the first step is not `instructions_hash`
plus the offered tools' snippets as a manifest verified for the run
names them), the neutral **prompt changed at this step** when a changed
tool set may explain the move (a ToolSource tool, an unverified
weft.json), **overridden by experiment** (the invoke_agent span carries
`weft.override.instructions`; the story view reads the spans once the
run ends) and **catalog changed at this step**; a read-scoped token
sees the `hidden` badge and nothing else
(`examples/studio-local`'s PrepareStep trim is the live demo):

![run page with the trace, light theme](screenshots/run-light.png)

Sessions list a thread's turns in order with usage and the newest
status; `/live` shows everything streaming right now; `/traces/{id}`
renders any trace — a stock OTel GenAI app's included — as a span tree
plus a chat view when semconv content was captured.

Every view is a URL: filters, `view=story|raw` (and `raw=doc`), the
selected span `sel` and detail mode `d`, the axis, the selected step,
and the replay position `t` all live in search params. Keyboard:
`⌘K` jumps to any recent run, `/` filters, `j`/`k` move, `enter`
opens, `e`/`s`/`r` switch trace/story/raw, `space` replays, `[`/`]`
jump by step or tool event, `,`/`.` move one event, `?` lists
everything. Keys fire only on a bare press outside a text box.

## The API (S4.2/S4.3)

JSON under `{base}api/`: `meta` (versions, `db` {kind, path, size —
path and size to loopback and the server token only}, `pid`,
`ingest_open`, `interrupted_after_ms`, capabilities, `capabilities_off`
{capability: why}, `manifest_sources`), `runs` (agent,
status, session, public id, playground, parent and `tag.<k>=<v>`
filters, cursor-paged; top-level only by default — `parent=<run id>`
lists one run's subagent children, `all=1` (or `parent=*`) every run,
children included),
`runs/{id}` (the row and the subagent children, each child row with its own `holes` — never events; with
`delta_count` (the streamed deltas, counted and never stored),
`instructions_hash`, `catalog_hash`, `request_count` and, for a run
written before ADR 0028, `requests_badge: "not_recorded"`; and
`compactions` — ADR 0028 §8's run-scope views `{scope: "run", index,
step, from_seq, to_seq, hash, replaced, entries}` and thread's session
markers `{scope: "session", hash, replaced, entries, tokens_before?,
tokens_after?, reason}`, counts and hashes only, `[]` when none),
`runs/{id}/events?after=&limit=` (the paged durable stream),
`runs/{id}/transcript` (the messages bodies), `runs/{id}/spans` (for a
read-scoped panel token — here, in `traces/{trace_id}` and in the
export alike — without the tool names a run's overrides put on its
invoke_agent span: `weft.override.tools`, `park_on`,
`park_all_except`, and a named `tool_choice` cut to its mode),
`runs/{id}/requests?step=&from=&limit=&refs=1` (one row per model-call
attempt, the prompt and catalog its hashes name inline unless `refs=1`,
`next_from` while pages are full) and `runs/{id}/tools` (the run's
catalogs by hash) — both under the `requests` capability, refused to a
read-scoped panel token (403, `badge: "hidden"`), and badged
`not_recorded` / `stripped` rather than empty when there is nothing to
show for a reason (a child run id whose last segment is itself one of
these sub-route names — `events`, `transcript`, `spans`, `requests`,
`tools`, `export`, `logs` — is shadowed by the route; provider call ids never collide),
`runs/{id}/logs?from=&limit=&severity=` (the app's own log lines — the
non-weft records an `slog` bridge or the OTel Logs API emitted under one
of the run's spans, or an app span below one — in time order, `{index,
time, severity, severity_number, body, attrs, span_id?}`, `next_from`
while pages are full; `severity` keeps a level and above without
renumbering; under the `logs` capability; `holes: [{hole, reason,
fix?}]` lists every hole of the page (`[]` when none) and
`badge`/`reason`/`fix` repeat the first: `not_recorded` (a run recorded
without a tracer), `truncated` (the run's traces hold more than 10 000
log lines in its window — the cap applies before attribution, so a
flooding subagent or sibling counts, and later lines of this run may be
missing), `gap` (lines in the run's trace and window name a span that
is not stored — dropped, or still open — and may be this run's or
another's); a running run's page is `partial: true` with a
`partial_reason` — lines under in-flight spans appear once those spans
end, and indexes may shift — a live condition, not a hole, so
`partial` can appear with no badge (then `reason` carries it too); app logs may carry prompts, so a
read-scoped panel token is refused them, 403 with `badge: "hidden"`),
`runs/{id}/steps/{n}` (one step assembled server-side, `n` its ordinal,
under the `steps` capability: the step's status, timing, the model
requested and the one that answered, attempt 1's request row, every
attempt with its model and outcome, `messages_in`, its events, its tool
calls with their spans and results, the child runs they started, usage,
the compaction view it saw and `holes` — every absent block a badge
with its reason and fix; scoped like `events`, except that a read-scoped
panel token gets the request block as `{badge: "hidden", …}` inside a
200; a step past the run's last, or not yet started, is 404),
`runs/{id}/export?format=json|jsonl|otlp|wefttest` (the whole run as
one download, `Content-Disposition: attachment; filename="<run
id>.<ext>"`, under the `export` capability: `json` is one document —
the run as `runs/{id}` serves it, every event, the transcript batches,
the compactions, the request records with the prompts and catalogs
they name keyed by hash, the spans and `holes`, every absent block
badged; `jsonl` is the same records one per line, each with a
`"record"` kind, in a stable order; `otlp` is
`{"logs": ExportLogsServiceRequest, "traces": ExportTraceServiceRequest}`
in OTLP/JSON, which `POST /v1/logs` and `/v1/traces` read back into the
same run; `wefttest` is the run's model calls as replay fixtures,
zipped flat — unzip into `testdata/<TestName>/` and `wefttest.Replay`
answers, a step whose request carried a compaction view keyed on it
and noted `compacted_at`. The run block's children carry their `holes`
as `runs/{id}` serves them. A read-scoped panel token gets `json` and
`jsonl` with the request block `{badge: "hidden", …}` and each
compaction view's `messages` null under the same badge; `otlp` and
`wefttest` are 403 with that badge),
`traces/{trace_id}` (any trace), `sessions`, `sessions/{id}` (turns in
order), `sessions/{id}/public_id` (the reverse of `public/`: `{session_id,
public_id}`, the public id `thread.PublicID` stamped on the session's
turns — MAX(`weft.public_id`) over its top-level turns: one value per
thread session; a session whose turns carry several answers the
greatest, unbadged; `public_id: ""` with `badge: "not_recorded"` when the session was
created without one, 404 for an unknown session; the dev token's and
setup A's alone — every panel token, read or playground, its own
session included, is 403 with `badge: "hidden"`; session ids carry no
`/`, so the sub-route shadows none), `public/{public_id}`, `manifest` (it carries the agents' system
prompts: 403 with `badge: "hidden"` to a read-scoped panel token),
`POST /api/panel-tokens`

(mint; the panel's scoped tokens — see the devtools panel above), and
`GET /api/live` — the SSE
stream whose frame ids are the hub's Seq: exactly one selector
(`run`/`session`/`public_id`/`agent`), `kinds` over
event/delta/messages/run (default `event,run`; deltas are opt-in,
heartbeats never), a ping every 15 s, `Last-Event-ID` resume with the
gap backfilled from the database and deduped on `(run, kind, pos)`,
and `event: overflow` when a slow subscriber's queue drops it. A token
travels in the `Authorization` header only — `?token=` is refused on
every `/api` route, in every setup (401 naming the grant) — so a
browser's `EventSource`, which cannot set headers,
opens the stream with a grant: `POST /api/live-grant` (authenticated
like every route; the selector and `kinds` as a JSON body
`{"run":"r_1","kinds":"event,run"}` or as the query, not both) answers
`{sig, exp}`, and `GET /api/live?run=r_1&kinds=event,run&sig=<sig>`
opens that one stream as the identity that asked. The sig is an
HMAC-SHA256 over that identity (the server token, a panel token's
public id and scope, or setup A's open API), the selector, the kinds
set (order-free) and `exp` — 60 s, never past a panel token's own
expiry; another selector, another kinds set, a tampered or an expired
sig is 401, and a panel token is refused a stream outside its public
id at grant time (403), as the stream would refuse it. Without a
`Token` the key is random per process: one mechanism in every setup.
A grant opens one stream within 60 s; a panel token's stream (opened
with a grant or with the bearer) ends at the token's expiry with one
final `event: expired` frame (`data: {}`) and closes; a server
token's does not (nor setup A's). After `expired`, reopen only with a
bearer that is still valid — a new panel token, then a new grant.
Both clients do exactly this (`lib/live.ts`'s `openLive` for the UI,
`openPanelLive` for the panel): a grant before every connection, the
browser's own `Last-Event-ID` reconnect kept inside the grant's 60 s —
measured on the page's clock from the answer's `Date` header, so a
skewed client clock neither spends every grant at once nor trusts a
spent one — a new grant (and a fresh connection, refetched) after it or
after a refused stream, on the backoff when it keeps failing. After
`expired` the UI asks again only with a fresh bearer (a pasted token);
the panel stops. A grant refused 403 (a panel token asking an agent's
stream: the live page, the runs list's follow) is asked once; the page
says "streaming needs the server token · polling" and polls. A fresh
connection carries no resume cursor — the header is the only way
`Last-Event-ID` is read — so the clients refetch pages instead, the
run page's tail on its first open too. The UI adopts a token from the
link's `#token=` fragment only; a `?token=` in the address is stripped,
never kept.
The `#token=` fragment `weft open` and `weft studio --open` hand the
browser never reaches the server; the UI reads it. OTLP
ingest is `POST /v1/traces` and `/v1/logs` (protobuf and JSON, gzip,
16 MiB after decompression, publish-then-write, 503 on a write failure
so the exporter retries). Under `Playground(true)` the playground's
routes join (`GET /api/runtimes`, `POST /api/playground/runs`, `GET
/api/playground/commands/{id}`, `POST /api/runs/{id}/approvals`,
`POST /api/playground/fixtures`, `GET/POST /api/experiments`, `GET
/api/experiments/{id}`, `PUT /api/runtimes/{id}/breakpoints`, `POST
/api/runs/{id}/steer` — see the playground above) beside the runtime
link's own (`POST /api/runtime/register`, `GET /api/runtime/commands`
SSE, `POST /api/runtime/acks` — server token only, never a panel
token); `/panel.js` serves the devtools panel bundle — static and
unauthenticated; `/panel-config.json` (`{endpoint, version,
capabilities}`, capability `panel-config`) answers a loopback (or
`AllowOrigins`) Host and a same-origin, `AllowOrigins` or loopback
Origin only, else 404.

Capabilities are computed from the registered route groups
(`routes.go`) — never hard-coded: `live` (the stream and its grant),
`ingest`, `auth` (with a
token), and `runtimes`, `breakpoints`, `steer` + `playground` under
`Playground(true)`, plus anything a hosting wrapper declares with
`studio.Capabilities(…)`.
The panel group registers always on and names no capability;
`panel.go` and `playground.go` add their groups through the package's
hooks — nothing edits `routes.go`.

Errors are `{"error": {"code", "message"}}` with the codes `not_found`,
`bad_request`, `unauthorized`, `forbidden`, `method_not_allowed`,
`conflict`, `unsupported`, `unavailable`, `internal`.

## Contributing to the UI

The web app lives in [web/](./web) — TanStack Start in SPA mode,
React, shadcn/ui on Tailwind v4, built with Bun. Users never need it:
the build output is committed at [dist/](./dist) and embedded with
`go:embed`.

```sh
make studio-build   # cd studio/web && bun install && bun run build
make studio-check   # rebuild, prove dist is fresh, check the 600 KiB gzip budget, typecheck + test
```

`make studio-check` is the freshness gate (ADR 0018 §4): it fails if
`dist/` does not match `web/` or if the gzipped total exceeds 600 KiB
(currently ~400 KiB: the app's ~363 plus the panel bundle's ~36.4).
The build is deterministic — two builds from
one tree are byte-identical (`scripts/clean-dist.ts` pins the router's
prerender timestamp and keeps `<base href>` first in `<head>`).

The dev loop is two terminals: `go run ./studio/examples/basic
-serve` (API + embedded UI on :7331) and `cd studio/web && bun run
dev -- --base /studio/` (Vite on :3000 proxying `/studio/api`).

Import path: `github.com/weftgo/weft/studio`, a package of the
framework module (`go get github.com/weftgo/weft`); it imports `core`,
`obsdb` and, for its tests, `otel`. The binary is
[cmd/weft](../cmd/weft) (`go install
github.com/weftgo/weft/cmd/weft@latest`, `make studio-bin` in this
repo), the one place that imports the clickhouse driver.
