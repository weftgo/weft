package studio

// The Live page's cancel (plan H6): POST /api/runs/{id}/cancel asks
// the runtime that started a run to stop it. Studio never runs your
// agent, so it never kills one either: the runtime link already
// carries a cancel frame for a command (weft/runtime cancels that
// command's own context), and the run then ends as the core ends any
// canceled run — a *RunError with the partial transcript, its records
// saying so, its command finished failed. A playground verb: the
// route registers with the playground group (playground.go), under
// its own capability, "cancel".
//
// Runtime-started runs only. The app's own turns hold no handle Studio
// could reach (an agent is immutable after New, a session's writer is
// the app's): they are refused with a 409 until plan F4 gives the app
// a way to opt in.

import (
	"errors"
	"net/http"

	"github.com/weftgo/weft/obsdb"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// cancelResponse is the 202's body: what was asked to stop, and where.
type cancelResponse struct {
	RunID     string `json:"run_id"`
	CommandID string `json:"command_id"`
	RuntimeID string `json:"runtime_id"`
	State     string `json:"state"` // "cancelling": asked, not yet ended
}

// cancelling is the 202's state: the frame is on the runtime's stream;
// the run's end arrives through its records (and the live stream).
const cancelling = "cancelling"

// serveCancel is POST /api/runs/{id}/cancel (capability `cancel`).
// Auth is steer's and the approvals' — the playground's write rule
// (S4.6): the server token, or setup A's open API; a read-scoped panel
// token is refused; a playground-scoped one cancels inside its public
// id only (the command's scope, else the run row's; a run with none
// is outside every panel token). The scope is checked before anything
// says whether the run is the app's or finished, so a refusal never
// describes another public id's run.
func (s *Server) serveCancel(rs *linkruntime.RuntimeServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runID := r.PathValue("id")
		// A cancel is a write on the app's process: a read-scoped panel
		// token does not act (S4.6).
		if !mayAct(w, r) {
			return
		}
		_, started := rs.RuntimeOf(runID)
		row, dbErr := s.db.Run(r.Context(), runID)
		if dbErr != nil && !errors.Is(dbErr, obsdb.ErrNotFound) {
			dbError(w, r, "run", runID, dbErr)
			return
		}
		stored := dbErr == nil
		if !started && !stored {
			notFound(w, r, "unknown run "+runID)
			return
		}
		if pid := idFrom(r).panel; pid != nil {
			publicID := ""
			if started {
				publicID, _ = rs.PublicOf(runID)
			}
			if publicID == "" && stored {
				publicID = row.PublicID
			}
			if publicID != pid.PublicID {
				forbidden(w, r)
				return
			}
		}
		if !started {
			writeError(w, r, http.StatusConflict, "conflict",
				"run "+runID+" is the app's own run, not one a runtime started: cancel arrives with F4")
			return
		}
		if stored && (row.Status == obsdb.StatusSucceeded || row.Status == obsdb.StatusFailed) {
			writeError(w, r, http.StatusConflict, "conflict",
				"run "+runID+" already finished ("+string(row.Status)+"): there is nothing to cancel")
			return
		}
		t, err := rs.CancelRun(runID)
		if err != nil {
			switch {
			case errors.Is(err, linkruntime.ErrRunFinished):
				writeError(w, r, http.StatusConflict, "conflict",
					"run "+runID+" already finished: its command is no longer in flight")
			case errors.Is(err, linkruntime.ErrNotConnected):
				writeError(w, r, http.StatusServiceUnavailable, "unavailable",
					"runtime "+t.RuntimeID+" is not connected")
			case errors.Is(err, linkruntime.ErrUnknownRun):
				// Pruned between the two reads: no runtime holds it now.
				writeError(w, r, http.StatusConflict, "conflict",
					"run "+runID+" already finished: no runtime holds it")
			default:
				writeError(w, r, http.StatusInternalServerError, "internal", err.Error())
			}
			return
		}
		writeJSON(w, r, http.StatusAccepted, cancelResponse{
			RunID: t.RunID, CommandID: t.CommandID, RuntimeID: t.RuntimeID, State: cancelling,
		})
	}
}
