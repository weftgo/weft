package studio

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/weftgo/weft/obsdb"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// The playground routes (WEFT-PLAYGROUND.md §10.4, S4.2): Studio
// decides, the runtime in your app executes. This file is lane C2's
// one owned file in the studio package: it sets playgroundGroupHook
// through a package-level var initializer — no init(), no registry
// (routes.go's rule) — and the Playground(true) option enables it.
//
// Two capabilities ship here: "playground" (this group: the connected
// runtimes view, POST /api/playground/runs, GET /api/playground/
// commands/{id}) and "runtimes" (the runtime link's own routes,
// mounted from this group because the hook returns one group — the
// registration point is the same, only the bookkeeping differs).
//
// P0 is API only (§7's P0 row): no UI. Everything 8b implements later
// (transcript edits, the scripted engine, fork mode) is already in
// the wire schema and refused with "not yet available", so the shape
// is final now and clients do not break when they land.

// playgroundGroupInstalled wires the playground's group into New's
// registry through routes.go's hook — the one registration point step
// 6 left for lane C2 — via a package-level var initializer (no
// init(); routes.go documents the mechanism). The hook stays nil in a
// build without this file; registerGroups calls it only when
// Playground(true) asked for it.
var playgroundGroupInstalled = func() bool {
	playgroundGroupHook = func() routeGroup {
		return routeGroup{
			name:       "playground",
			capability: "playground",
			register:   registerPlayground,
		}
	}
	return true
}()

// The wiring's value is its side effect; this read keeps the var
// referenced for the unused checker (the var itself is the no-init
// mechanism routes.go documents, same as the panel's).
var _ = playgroundGroupInstalled

// registerPlayground mounts the playground routes and the runtime
// link. The link server is per-Server (a Studio embeds one), built
// here on Playground(true); Server.Runtime — the accessor S4.1 names
// for it — returns it.
func registerPlayground(mux *http.ServeMux, s *Server) {
	rs := linkruntime.New()
	s.runtimeSrv = rs
	mux.HandleFunc("GET /api/runtimes", s.serveRuntimes(rs))
	mux.HandleFunc("POST /api/playground/runs", s.servePlaygroundRun(rs))
	mux.HandleFunc("GET /api/playground/commands/{id}", s.servePlaygroundCommand(rs))
	// The runtime link's own block (§10.3), as its own capability.
	s.addGroup(routeGroup{
		name:       "runtimes",
		capability: "runtimes",
		register: func(mux *http.ServeMux, s *Server) {
			rs.Mount(mux)
		},
	})
}

// ── GET /api/runtimes ─────────────────────────────────────────────

// serveRuntimes answers §10.4's connected-runtimes view: every
// runtime that registered, with liveness and its agents. Open to any
// authenticated reader (a panel's read token included): this is the
// picker's data, not per-public-id content.
func (s *Server) serveRuntimes(rs *linkruntime.RuntimeServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, r, http.StatusOK, struct {
			Runtimes []linkruntime.RuntimeView `json:"runtimes"`
		}{rs.Snapshot()})
	}
}

// ── POST /api/playground/runs ─────────────────────────────────────

// runRequest is §5.1's command body. CommandID is optional: absent,
// Studio mints one; present, it is the caller's idempotency key — a
// reuse is 409 (§10.4), which is the rule a retried POST needs.
type runRequest struct {
	Runtime         string                       `json:"runtime"`
	CommandID       string                       `json:"command_id"`
	Agent           string                       `json:"agent"`
	Source          *linkruntime.SourceSpec      `json:"source"`
	Input           *string                      `json:"input"`
	Overrides       linkruntime.Overrides        `json:"overrides"`
	TranscriptEdits []linkruntime.TranscriptEdit `json:"transcript_edits"`
	Engine          string                       `json:"engine"`
	SideEffects     string                       `json:"side_effects"`
	Thread          string                       `json:"thread"`
	ExperimentID    string                       `json:"experiment_id"`
	PublicID        string                       `json:"public_id"`
}

// servePlaygroundRun validates the command against the runtime's
// registered copy (§5.1 and §10.4's table) and hands it to the link.
func (s *Server) servePlaygroundRun(rs *linkruntime.RuntimeServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req runRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			badRequest(w, r, "run body: "+err.Error())
			return
		}

		// What 8b defers: in the schema, refused on the wire.
		if len(req.TranscriptEdits) > 0 {
			badRequest(w, r, "transcript_edits are not yet available")
			return
		}
		switch req.Engine {
		case "", "live":
		case "scripted":
			badRequest(w, r, "engine scripted is not yet available")
			return
		default:
			badRequest(w, r, "unknown engine "+req.Engine)
			return
		}
		switch req.Thread {
		case "", "ephemeral":
		case "fork":
			badRequest(w, r, "thread fork is not yet available")
			return
		default:
			badRequest(w, r, "unknown thread mode "+req.Thread)
			return
		}
		switch req.SideEffects {
		case "", "substitute", "park", "allow":
		default:
			badRequest(w, r, "unknown side_effects mode "+req.SideEffects)
			return
		}

		if req.Runtime == "" {
			badRequest(w, r, "a run needs a runtime (the connected runtimes view lists them)")
			return
		}
		reg, ok := rs.Registration(req.Runtime)
		if !ok {
			notFound(w, r, "unknown runtime "+req.Runtime)
			return
		}
		if !rs.Connected(req.Runtime) {
			writeError(w, r, http.StatusServiceUnavailable, "unavailable",
				"runtime "+req.Runtime+" is not connected")
			return
		}
		agent, ok := reg.Agent(req.Agent)
		if !ok {
			notFound(w, r, "unknown agent "+req.Agent)
			return
		}

		// The source turn: from_step is 0+ and input may not replace
		// a continued turn's message (both 400, body-shape errors),
		// and the run must exist (404).
		if req.Source != nil && req.Source.RunID != "" {
			if req.Source.FromStep < 0 {
				badRequest(w, r, "source.from_step must be 0 or more")
				return
			}
			if req.Input != nil && *req.Input != "" && req.Source.FromStep > 0 {
				badRequest(w, r, "input replaces the turn's user message only when from_step is 0")
				return
			}
			if _, err := s.db.Run(r.Context(), req.Source.RunID); err != nil {
				if errors.Is(err, obsdb.ErrNotFound) {
					notFound(w, r, "unknown source run "+req.Source.RunID)
					return
				}
				dbError(w, r, "source run", req.Source.RunID, err)
				return
			}
		}

		// Overrides against the registered copy — narrowing only
		// (§6 rule 2): tools the manifest lacks are 400, a raised
		// limit or a refused side-effect tool is 403.
		manifestTools := agent.ManifestToolNames()
		known := make(map[string]bool, len(manifestTools))
		for _, name := range manifestTools {
			known[name] = true
		}
		enabled := req.Overrides.ToolsEnabled
		for _, name := range enabled {
			if !known[name] {
				badRequest(w, r, "tool "+name+" is not in agent "+agent.Name+"'s manifest")
				return
			}
		}
		if m := req.Overrides.Model; m != "" && !contains(agent.Models, m) && m != agent.ManifestModelName() {
			badRequest(w, r, "model "+m+" is neither the agent's own nor a registered alternate")
			return
		}
		switch req.Overrides.Thinking {
		case "", "off", "low", "medium", "high":
		default:
			badRequest(w, r, "unknown thinking level "+req.Overrides.Thinking)
			return
		}
		for key := range req.Overrides.Options {
			switch key {
			case "max_steps", "parallelism", "temperature":
			default:
				badRequest(w, r, "unknown option "+key)
				return
			}
		}
		if n := int(req.Overrides.Options["max_steps"]); n > 0 && agent.Limits.MaxSteps > 0 && n > agent.Limits.MaxSteps {
			writeError(w, r, http.StatusForbidden, "forbidden",
				"max_steps may only lower the agent's cap")
			return
		}
		if n := int(req.Overrides.Options["parallelism"]); n > 0 && agent.Limits.Parallelism > 0 && n > agent.Limits.Parallelism {
			writeError(w, r, http.StatusForbidden, "forbidden",
				"parallelism may only lower the agent's cap")
			return
		}
		if req.SideEffects == "allow" {
			touched := enabled
			if len(touched) == 0 {
				touched = manifestTools
			}
			for _, name := range touched {
				if !agent.IsAllowed(name) {
					writeError(w, r, http.StatusForbidden, "forbidden",
						"tool "+name+" is not opted in for real side effects")
					return
				}
			}
		}

		// A panel token may act inside its public id only, and a
		// read-scoped one may not act at all (S4.6).
		if id := idFrom(r); id.panel != nil {
			if id.panel.Scope != scopePlayground {
				forbidden(w, r)
				return
			}
			if req.PublicID != id.panel.PublicID {
				forbidden(w, r)
				return
			}
		}

		// The at-most-once id: a reuse is 409, whatever the body.
		if req.CommandID != "" {
			if _, exists := rs.CommandOf(req.CommandID); exists {
				writeError(w, r, http.StatusConflict, "conflict",
					"command id "+req.CommandID+" was already used")
				return
			}
		}

		cmd, err := rs.Enqueue(req.Runtime, linkruntime.Command{
			CommandID:       req.CommandID,
			Agent:           req.Agent,
			Source:          req.Source,
			Input:           req.Input,
			Overrides:       req.Overrides,
			TranscriptEdits: req.TranscriptEdits,
			Engine:          req.Engine,
			SideEffects:     req.SideEffects,
			Thread:          req.Thread,
			ExperimentID:    req.ExperimentID,
			Actor:           actorOf(r),
			PublicID:        req.PublicID,
		})
		if err != nil {
			switch {
			case errors.Is(err, linkruntime.ErrUnknownRuntime):
				notFound(w, r, "unknown runtime "+req.Runtime)
			case errors.Is(err, linkruntime.ErrNotConnected):
				writeError(w, r, http.StatusServiceUnavailable, "unavailable",
					"runtime "+req.Runtime+" is not connected")
			case errors.Is(err, linkruntime.ErrDuplicateCommand):
				writeError(w, r, http.StatusConflict, "conflict",
					"command id "+cmd.CommandID+" was already used")
			default:
				writeError(w, r, http.StatusInternalServerError, "internal", err.Error())
			}
			return
		}
		writeJSON(w, r, http.StatusAccepted, struct {
			CommandID string `json:"command_id"`
			State     string `json:"state"`
		}{cmd.CommandID, linkruntime.StateQueued})
	}
}

// actorOf names who triggered the run (§6 rule 4): the server token,
// a panel token's public id, or local (setup A's open API).
func actorOf(r *http.Request) string {
	if id := idFrom(r); id.panel != nil {
		return "panel:" + id.panel.PublicID
	} else if id.server {
		return "server"
	}
	return "local"
}

// contains reports whether s holds v.
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// ── GET /api/playground/commands/{id} ─────────────────────────────

// servePlaygroundCommand answers §10.4's lifecycle row — §10.5's five
// states with their timers running server-side. A panel token reads
// the commands of its own public id only.
func (s *Server) servePlaygroundCommand(rs *linkruntime.RuntimeServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		cmd, ok := rs.CommandOf(id)
		if ok {
			if pid := idFrom(r).panel; pid != nil && cmd.PublicID != pid.PublicID {
				forbidden(w, r)
				return
			}
		}
		st, err := rs.Command(id)
		if err != nil {
			notFound(w, r, "unknown command "+id)
			return
		}
		writeJSON(w, r, http.StatusOK, st)
	}
}
