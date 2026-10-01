package studio

import (
	"net/http"

	"github.com/weftgo/weft/studio/ingest"
	linkrt "github.com/weftgo/weft/studio/runtime"
)

// The route registry (S4.2): Studio's surface is a list of route
// groups, and api/meta's capabilities are computed from that list —
// never hard-coded. New registers the core groups below; the lanes
// that follow add theirs in their own files without editing this one:
//
//   - step 7 (lane C1) adds studio/panel.go, which sets
//     panelGroupHook through a package-level var initializer and so
//     registers the panel group (always on);
//   - step 8 (lane C2) adds studio/playground.go, which sets
//     playgroundGroupHook the same way; the Playground(true) option
//     is what enables it.
//
// A hook is nil until its file exists, which is why a step-6 build
// reports no panel or playground capability: the group is simply not
// registered. No init() anywhere — the var initializer is the whole
// mechanism, and New does the enabling.
var (
	// panelGroupHook builds the devtools panel's group (step 7).
	panelGroupHook func() routeGroup
	// playgroundGroupHook builds the playground's group (step 8).
	playgroundGroupHook func() routeGroup
)

// routeGroup is one capability's block of routes: a name for errors
// and logs, the capability api/meta reports when the group is
// registered ("" for groups that are always present and name nothing),
// and the registration that mounts its routes on the server's mux.
type routeGroup struct {
	name       string
	capability string
	register   func(mux *http.ServeMux, s *Server)
}

// addGroup registers g's routes and records the group for
// meta.capabilities. New calls it for the core groups; the hooks above
// call it for the step-7/8 groups.
func (s *Server) addGroup(g routeGroup) {
	if g.register != nil {
		g.register(s.mux, s)
	}
	s.groups = append(s.groups, g)
}

// capabilityList names what the registered groups provide plus
// anything the hosting wrapper declared with Capabilities(...): the
// UI gates every deployment-specific screen on these names
// (ADR 0018 §8).
func (s *Server) capabilityList() []string {
	caps := make([]string, 0, len(s.groups)+len(s.capabilities))
	for _, g := range s.groups {
		if g.capability != "" {
			caps = append(caps, g.capability)
		}
	}
	caps = append(caps, s.capabilities...)
	return caps
}

// registerGroups mounts the core route groups (S4.2), then the
// optional ones their hooks provide. The playground group registers
// only when Playground(true) asked for it; the panel group is always
// on once its file exists. Runtime-link and experiment routes are
// step 8 and are absent here.
func (s *Server) registerGroups() {
	// The read API: meta, manifest, runs, run detail, events,
	// transcript, spans, traces, sessions, public-id resolution.
	// Always present, so it names no capability.
	s.addGroup(routeGroup{
		name:     "api",
		register: registerReadAPI,
	})
	// The live stream (S4.5).
	s.addGroup(routeGroup{
		name:       "live",
		capability: "live",
		register: func(mux *http.ServeMux, s *Server) {
			mux.HandleFunc("GET /api/live", s.serveLive)
		},
	})
	// OTLP ingest (S4.4), unless NoIngest turned the receiver off.
	if !s.noIngest {
		s.addGroup(routeGroup{
			name:       "ingest",
			capability: "ingest",
			register:   registerIngest,
		})
	}
	// Panel-token minting (S4.6): present exactly when a server token
	// is configured — there is nothing to sign without one.
	if s.token != "" {
		s.addGroup(routeGroup{
			name:       "auth",
			capability: "auth",
			register: func(mux *http.ServeMux, s *Server) {
				mux.HandleFunc("POST /api/panel-tokens", s.servePanelTokens)
			},
		})
	}
	if panelGroupHook != nil {
		s.addGroup(panelGroupHook()) // step 7: always on
	}
	if s.playground && playgroundGroupHook != nil {
		s.addGroup(playgroundGroupHook()) // step 8: Playground(true)
	}
	// Unknown API and ingest paths are 404s in the error shape, never
	// the SPA shell: a mis-routed API call must fail loudly, not
	// hydrate. (Checked in serveUI, the "/" fallback, so a wrong-method
	// request still gets ServeMux's own 405 with Allow.)
}

// serveAPINotFound answers any registered-group path the groups do
// not cover.
func (s *Server) serveAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotFound, "not_found",
		"no such api route "+r.URL.Path)
}

// registerReadAPI mounts the S4.2 read routes. Run ids contain slashes
// (a subagent's child id is <parent>/<step>/<callID>), so the run
// subtree is one handler that takes everything after /api/runs/ as
// the id, with at most a trailing /events, /transcript or /spans
// segment; session, trace and public ids never carry one.
func registerReadAPI(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("GET /api/meta", s.serveMeta)
	mux.HandleFunc("GET /api/manifest", s.serveManifest)
	mux.HandleFunc("GET /api/runs", s.serveRuns)
	mux.HandleFunc("GET /api/runs/", s.serveRunRoutes)
	mux.HandleFunc("GET /api/sessions", s.serveSessions)
	mux.HandleFunc("GET /api/sessions/", s.serveSessionRoutes)
	mux.HandleFunc("GET /api/traces/", s.serveTrace)
	mux.HandleFunc("GET /api/public/", s.servePublic)
}

// registerIngest mounts the OTLP/HTTP receiver (studio/ingest, S4.4).
func registerIngest(mux *http.ServeMux, s *Server) {
	ok := s.ingestAuthorized
	mux.HandleFunc("POST /v1/traces", ingest.Traces(s.db, s.live, ok))
	mux.HandleFunc("POST /v1/logs", ingest.Logs(s.db, s.live, ok))
}

// RuntimeServer is the runtime link server's in-process side
// (studio/runtime): the registry of connected runtimes that
// weft/runtime's runtime.Local(srv) drives and Server.Runtime
// returns — an alias, so the S4.1 name stays in this package while
// the type lives where the routes do. Nil on a Server built without
// Playground(true); the runtime-link routes register with the
// playground group.
type RuntimeServer = linkrt.RuntimeServer
