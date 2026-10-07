package studio

// The debugger's rungs 3–4 on runtime-started runs (WEFT-DEVTOOLS
// §8.3/§8.4, ADR 0024 D7): breakpoints park calls to named tools on
// every run the runtime starts from then on, and steer delivers one
// user message into a run it holds — weft.Steering on an ephemeral
// run, thread's Steer policy on a fork it owns. The app's own turns
// are viewer-only: an agent is immutable after New and a session's
// writer is the app's, so a run no runtime started is refused here,
// and meta (and both UIs) say so (PQ7).

import (
	"errors"
	"net/http"

	"github.com/weftgo/weft/obsdb"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// breakpointsRequest is PUT /api/runtimes/{id}/breakpoints: the tools
// to break on (an empty set clears).
type breakpointsRequest struct {
	Tools []string `json:"tools"`
}

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
		if err := rs.SetBreakpoints(id, req.Tools); err != nil {
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

// steerRequest is POST /api/runs/{id}/steer: one user message.
type steerRequest struct {
	Message string `json:"message"`
}

// serveSteer is the rung-4 verb (capability `steer`). The run must be
// one a runtime started; the app's own turns are refused — viewer-only
// (D7, PQ7).
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
			// app's own turns exist and stay viewer-only (D7, PQ7).
			if _, err := s.db.Run(r.Context(), runID); err != nil {
				if errors.Is(err, obsdb.ErrNotFound) {
					notFound(w, r, "unknown run "+runID)
					return
				}
				dbError(w, r, "run", runID, err)
				return
			}
			writeError(w, r, http.StatusForbidden, "forbidden",
				"only runs a runtime started can be steered — the app's own turns are viewer-only (PQ7)")
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
		// A fork-mode run is the runtime's own thread turn, and the
		// runtime refuses to steer it: thread would turn a steer it
		// cannot deliver into a follow-up turn outside the playground's
		// park rule. Answering steered would tell the user it landed.
		if rs.ForkRun(runID) {
			writeError(w, r, http.StatusConflict, "conflict",
				"run "+runID+" is a fork-mode turn: steering reaches ephemeral runs only — send the message as the fork's next input instead")
			return
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
