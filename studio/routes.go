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
// A hook is nil in a build without its file, which then reports no
// panel or playground capability: the group is simply not
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

// runRoute is one /api/runs/{id}/<ext> sub-route: run ids contain
// slashes, so these cannot be mux patterns of their own, and a group
// registers them through addRunRoute instead.
type runRoute struct {
	ext   string
	serve func(http.ResponseWriter, *http.Request, string)
	// item, when set, serves /api/runs/{id}/<ext>/{item} instead: one
	// item of a run's collection (a step by its ordinal).
	item func(w http.ResponseWriter, r *http.Request, id, item string)
}

// addRunRoute registers a GET /api/runs/{id}/<ext> sub-route, which
// serveRunRoutes dispatches by suffix. So a child run id whose last
// segment equals a sub-route name (events, transcript, spans, requests,
// tools, export, logs) is shadowed by that route: its run document is unreachable
// at /api/runs/{id}. A child id's last segment is the provider's tool
// call id, and providers' ids never take those shapes.
func (s *Server) addRunRoute(ext string, serve func(http.ResponseWriter, *http.Request, string)) {
	s.runRoutes = append(s.runRoutes, runRoute{ext: ext, serve: serve})
}

// addRunItemRoute registers a GET /api/runs/{id}/<ext>/{item} sub-route
// (one item of a run's collection: a step by its ordinal), which
// serveRunRoutes dispatches when the path's next-to-last segment is
// ext. The same shadowing caveat as addRunRoute's applies to a child
// run id whose last two segments read <ext>/<item>; a child id's
// segments are <step>/<callID>, so its next-to-last is a number.
func (s *Server) addRunItemRoute(ext string, serve func(w http.ResponseWriter, r *http.Request, id, item string)) {
	s.runRoutes = append(s.runRoutes, runRoute{ext: ext, item: serve})
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
	// transcript, spans, traces, sessions, public-id resolution (and
	// its reverse, sessions/{id}/public_id, the dev token's alone).
	// Always present, so it names no capability.
	s.addGroup(routeGroup{
		name:     "api",
		register: registerReadAPI,
	})
	// The request record's read routes (ADR 0028 §10, A1): every
	// model-call attempt with the prompt and catalog it named, and the
	// run's catalogs. Always present now that obsdb.DB reads them; the
	// capability is what the UIs gate the Request pane on.
	s.addGroup(routeGroup{
		name:       "requests",
		capability: "requests",
		register: func(_ *http.ServeMux, s *Server) {
			s.addRunRoute("requests", s.serveRunRequests)
			s.addRunRoute("tools", s.serveRunTools)
		},
	})
	// One step assembled server-side (plan A7, A4's attempts, A10's
	// children): always present; the capability is what the UIs gate
	// the step card's single read on.
	s.addGroup(routeGroup{
		name:       "steps",
		capability: "steps",
		register: func(_ *http.ServeMux, s *Server) {
			s.addRunItemRoute("steps", s.serveRunStep)
		},
	})
	// The run export (plan A7): the whole run as one download, in
	// json, jsonl, otlp or wefttest. Always present; the capability is
	// what the UIs gate their export buttons on.
	s.addGroup(routeGroup{
		name:       "export",
		capability: "export",
		register: func(_ *http.ServeMux, s *Server) {
			s.addRunRoute("export", s.serveRunExport)
		},
	})
	// The run's app logs (plan A7): the non-weft log records attributed
	// to the run through its spans. Always present; the capability is
	// what the UIs gate their logs pane on.
	s.addGroup(routeGroup{
		name:       "logs",
		capability: "logs",
		register: func(_ *http.ServeMux, s *Server) {
			s.addRunRoute("logs", s.serveRunLogs)
		},
	})
	// The live stream (S4.5) and its grant (plan C5): the stream's
	// credential is its other half, so one group, one capability.
	s.addGroup(routeGroup{
		name:       "live",
		capability: "live",
		register: func(mux *http.ServeMux, s *Server) {
			mux.HandleFunc("GET /api/live", s.serveLive)
			mux.HandleFunc("POST /api/live-grant", s.serveLiveGrant)
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
	s.addGroup(panelConfigGroup()) // plan B3: GET /panel-config.json, always on
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
// the id, with at most one trailing segment a group registered through
// addRunRoute (/events, /transcript, /spans; /requests and /tools from
// the requests group); session, trace and public ids never carry one.
func registerReadAPI(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("GET /api/meta", s.serveMeta)
	mux.HandleFunc("GET /api/manifest", s.serveManifest)
	mux.HandleFunc("GET /api/runs", s.serveRuns)
	mux.HandleFunc("GET /api/runs/", s.serveRunRoutes)
	mux.HandleFunc("GET /api/sessions", s.serveSessions)
	mux.HandleFunc("GET /api/sessions/", s.serveSessionRoutes) // {id} and {id}/public_id
	mux.HandleFunc("GET /api/traces/", s.serveTrace)
	mux.HandleFunc("GET /api/public/", s.servePublic)
	s.addRunRoute("events", s.serveRunEvents)
	s.addRunRoute("transcript", s.serveRunTranscript)
	s.addRunRoute("spans", s.serveRunSpans)
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
