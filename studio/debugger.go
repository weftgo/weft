package studio

// The debugger's rungs 3–4 on runtime-started runs (WEFT-DEVTOOLS
// §8.3/§8.4, ADR 0024 D7): breakpoints park calls to named tools on
// every run the runtime starts from then on, and steer delivers one
// user message into a run it holds — core.Steering on an ephemeral
// run, thread's Steer policy on a fork it owns. The app's own turns
// are viewer-only unless the app opted in (plan F4): an agent is
// immutable after New and a session's writer is the app's, so a run no
// runtime started is steered only through a runtime installed with
// runtime.Sessions — the app's own Session does the writing — and is
// refused 403 "app-owned turn" otherwise; meta's debug_scope says which.

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/weftgo/weft/obsdb"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// breakpointsRequest is PUT /api/runtimes/{id}/breakpoints: the tools
// to break on (an empty set clears) and, optionally, the surface that
// sends them (U30.2, UX-A-33: /api/runtimes' breakpoints_surface, "set
// by <surface>"; absent reads "api"). The answer carries the tools
// alone.
type breakpointsRequest struct {
	Tools   []string `json:"tools"`
	Surface string   `json:"surface,omitempty"`
}

// surfaceRE is a surface name: a short lowercase word ("runtimes").
var surfaceRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// serveBreakpoints is the rung-3 verb (capability `breakpoints`).
func (s *Server) serveBreakpoints(rs *linkruntime.RuntimeServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// A runtime-wide effect — it parks every future run of the
		// runtime — so it is not public-id-shaped and S4.6's scoping
		// cannot apply: a panel token is refused outright, the server
		// token or setup A's open API passes.
		if idFrom(r).panel != nil {
			writeError(w, r, http.StatusForbidden, "forbidden",
				"breakpoints park every future run of the runtime: the server token, not a panel token")
			return
		}
		id := r.PathValue("id")
		var req breakpointsRequest
		if !decodeBody(w, r, "breakpoints body", &req) {
			return
		}
		reg, ok := rs.Registration(id)
		if !ok {
			notFound(w, r, "unknown runtime "+id)
			return
		}
		// Every name must be a tool of some registered agent: a
		// breakpoint on a name nobody exposes can never fire and is a
		// typo, not a rule.
		known := map[string]bool{}
		for _, a := range reg.Agents {
			for _, t := range a.ManifestToolNames() {
				known[t] = true
			}
		}
		for _, t := range req.Tools {
			if !known[t] {
				badRequest(w, r, "tool "+t+" is not registered by any agent of runtime "+id)
				return
			}
		}
		if req.Surface != "" && !surfaceRE.MatchString(req.Surface) {
			badRequest(w, r, "surface must be a short lowercase word ([a-z][a-z0-9_-]{0,31}), e.g. runtimes")
			return
		}
		if err := rs.SetBreakpointsFrom(id, req.Tools, req.Surface); err != nil {
			switch {
			case errors.Is(err, linkruntime.ErrUnknownRuntime):
				notFound(w, r, "unknown runtime "+id)
			case errors.Is(err, linkruntime.ErrNotConnected):
				writeError(w, r, http.StatusServiceUnavailable, "unavailable",
					"runtime "+id+" is not connected")
			default:
				writeError(w, r, http.StatusInternalServerError, "internal", err.Error())
			}
			return
		}
		writeJSON(w, r, http.StatusOK, breakpointsRequest{Tools: rs.BreakpointsOf(id)})
	}
}

// serveForgetRuntime is DELETE /api/runtimes/{id} (capability
// runtime_forget, U20.1): 204 once the runtime is gone from the list
// and the store; 409 while it is connected or a queued or accepted
// command is addressed to it; 404 for an id nothing registered. Like
// breakpoints it is runtime-wide, not public-id-shaped: a panel token
// is refused outright.
func (s *Server) serveForgetRuntime(rs *linkruntime.RuntimeServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if idFrom(r).panel != nil {
			writeError(w, r, http.StatusForbidden, "forbidden",
				"forgetting a runtime changes Studio's list for everyone: the server token, not a panel token")
			return
		}
		id := r.PathValue("id")
		switch err := rs.Forget(id); {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, linkruntime.ErrUnknownRuntime):
			notFound(w, r, "unknown runtime "+id)
		case errors.Is(err, linkruntime.ErrConnected):
			writeError(w, r, http.StatusConflict, "conflict",
				"runtime "+id+" is connected: only a runtime that is gone is forgotten")
		case errors.Is(err, linkruntime.ErrRuntimeBusy):
			writeError(w, r, http.StatusConflict, "conflict",
				"runtime "+id+" still has queued or accepted commands: forget it once they resolve")
		default:
			writeError(w, r, http.StatusInternalServerError, "internal", err.Error())
		}
	}
}

// steerRequest is POST /api/runs/{id}/steer: one user message.
type steerRequest struct {
	Message string `json:"message"`
}

// appSteerRefused is the runtime's refusal of a steer on an app turn
// that is not running (runtime/sessions.go), word for word.
const appSteerRefused = "no running turn to steer"

// serveSteer is the rung-4 verb (capability `steer`). A run a runtime
// started is steered through that runtime (202 {steered}); an app-owned
// turn through a runtime that registered runtime.Sessions (202 with the
// command to poll, appturn.go), else 403 "app-owned turn" (plan F4).
func (s *Server) serveSteer(rs *linkruntime.RuntimeServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runID := r.PathValue("id")
		var req steerRequest
		if !decodeBody(w, r, "steer body", &req) {
			return
		}
		if req.Message == "" {
			badRequest(w, r, "steer body: message is required")
			return
		}
		// Steering writes into a run: a read-scoped panel token does
		// not (S4.6).
		if !mayAct(w, r) {
			return
		}
		runtimeID, ok := rs.RuntimeOf(runID)
		if !ok {
			// Not a runtime-started run. An unknown id is a 404; the
			// app's own turns are steered only through a runtime that
			// registered runtime.Sessions (plan F4, appturn.go), inside
			// a panel token's public id like every other verb.
			row, err := s.db.Run(r.Context(), runID)
			if err != nil {
				if errors.Is(err, obsdb.ErrNotFound) {
					notFound(w, r, "unknown run "+runID)
					return
				}
				dbError(w, r, "run", runID, err)
				return
			}
			if pid := idFrom(r).panel; pid != nil && row.PublicID != pid.PublicID {
				forbidden(w, r)
				return
			}
			target, ok := s.appTurnRuntime(w, r, rs, row.RunRow)
			if !ok {
				return
			}
			// A turn that has ended has nothing to steer into: refused
			// now, 409, in the runtime's words — not accepted and then
			// failed on the command's row (UX-H-19). A row still reading
			// running goes to the runtime, whose session decides.
			if row.Pending > 0 {
				// Parked at the approval boundary: nothing runs to steer
				// into; the next move is a decision (cancel's rule).
				writeError(w, r, http.StatusConflict, "conflict",
					appSteerRefused+": run "+runID+" is parked: decide its pending calls (approve/deny) instead")
				return
			}
			if row.Status == obsdb.StatusSucceeded || row.Status == obsdb.StatusFailed {
				writeError(w, r, http.StatusConflict, "conflict",
					appSteerRefused+": run "+runID+" already finished ("+string(row.Status)+")")
				return
			}
			sc := appSessionCommand(r, linkruntime.VerbSteer, row.RunRow)
			sc.Message = req.Message
			enqueueAppTurn(w, r, rs, target, sc)
			return
		}
		// A panel token steers inside its public id only (S4.6) — the
		// approval route's rule: the scope the command carried, else
		// the run row's own public id, and empty is a refusal (a run
		// with no public id is outside every panel token).
		if pid := idFrom(r).panel; pid != nil {
			publicID, _ := rs.PublicOf(runID)
			if publicID == "" {
				if row, err := s.db.Run(r.Context(), runID); err == nil {
					publicID = row.PublicID
				}
			}
			if publicID != pid.PublicID {
				forbidden(w, r)
				return
			}
		}
		if err := rs.Steer(runtimeID, runID, req.Message); err != nil {
			switch {
			case errors.Is(err, linkruntime.ErrUnknownRuntime):
				notFound(w, r, "unknown runtime "+runtimeID)
			case errors.Is(err, linkruntime.ErrNotConnected):
				writeError(w, r, http.StatusServiceUnavailable, "unavailable",
					"runtime "+runtimeID+" is not connected")
			default:
				writeError(w, r, http.StatusInternalServerError, "internal", err.Error())
			}
			return
		}
		writeJSON(w, r, http.StatusAccepted, struct {
			Steered bool `json:"steered"`
		}{true})
	}
}
