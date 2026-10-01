package studio

import (
	"embed"
	"net/http"
)

// The devtools panel (WEFT-DEVTOOLS.md §5.1, step 7): the committed
// web build's library-mode artifact, dist/panel/panel.js — a
// self-contained custom element the page loads with one script tag.
// V2: the panel's files are served by your backend (or by this
// embedded Studio), never a CDN.
//
// This file registers the panel's route group through routes.go's
// hook — the one registration point step 6 left for lane C1 — via a
// package-level var initializer (no init(); routes.go documents the
// mechanism). The group is always on once this file exists and names
// no capability: the panel is a client of the API, gated by the
// capabilities meta reports, not a capability of its own (§8.1's
// rung-1 capability, live, is the live group's).

// panelJS is the committed bundle (studio/web, make studio-build):
// byte-identical to dist/panel/panel.js, which the freshness gate
// (make studio-check) keeps in sync.
//
//go:embed all:dist/panel/panel.js
var panelJS embed.FS

// panelGroupInstalled wires the panel's group into New's registry.
var panelGroupInstalled = func() bool {
	panelGroupHook = func() routeGroup {
		return routeGroup{
			name: "panel",
			register: func(mux *http.ServeMux, s *Server) {
				mux.HandleFunc("GET /panel.js", s.servePanelJS)
			},
		}
	}
	return true
}()

// The wiring's value is its side effect; this read keeps the var
// referenced for the unused checker (the var itself is the no-init
// mechanism routes.go documents, same as the playground's).
var _ = panelGroupInstalled

// servePanelJS answers GET /panel.js (S4.2): static, versioned, no
// auth — the page that includes it may be any page on any origin
// (setups A/B/C), and the panel itself carries the API token, never
// the script URL. The bundle carries the studio version it was built
// against; the panel refuses to render against a newer Studio
// (WEFT-DEVTOOLS §5.1), so a stale panel.js in a page fails loudly in
// the dock, not silently as wrong data.
func (s *Server) servePanelJS(w http.ResponseWriter, r *http.Request) {
	b, err := panelJS.ReadFile("dist/panel/panel.js")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal",
			"panel.js is missing from the build — run 'make studio-build' and commit studio/dist/panel")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	// The URL is versioned by the studio build that serves it (the
	// module version is stamped into the bundle), so revalidation is
	// cheap and correctness never depends on a stale cache.
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(b)
}
