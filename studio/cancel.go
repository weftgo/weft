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
// Runtime-started runs are cancelled through the link's cancel frame.
// The app's own turns hold no handle Studio could reach (an agent is
// immutable after New, a session's writer is the app's): they are
// refused 403 "app-owned turn" — as steer and approvals are — unless a
// runtime of the run's service registered runtime.Sessions (plan F4);
// then the cancel goes to it as a session command, which today's
// runtime refuses on the command's row: thread has no per-turn cancel.

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
// says whether the run is the app's, parked or finished; an unknown id
// is 404 before the scope check (as steer answers it), which says only
// that no run has that id — run ids are ULIDs, not guessable.
//
// Best effort: the cancel frame rides the runtime's command stream, and
// a frame lost to a stream reconnect is not retried — cancel again. A
// parked run (pending calls, the approval boundary) is not cancelled:
// it is over until decided, so the 409 says to approve or deny instead.
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
			// The app's own run (stored: !started && !stored is the 404
			// above): through a runtime installed with runtime.Sessions,
			// else 403 "app-owned turn" — the steer and approvals answer
			// (plan F4, appturn.go). The runtime refuses it today, "cancel
			// is not supported on an app turn" (thread has no per-turn
			// cancel), on the command's row.
			target, ok := appTurnRuntime(w, r, rs, row.RunRow)
			if !ok {
				return
			}
			enqueueAppTurn(w, r, rs, target, appSessionCommand(r, linkruntime.VerbCancel, row.RunRow))
			return
		}
		if stored && row.Pending > 0 {
			// Parked at the approval boundary: the run ended successfully
			// with its calls pending; the next move is a decision.
			writeError(w, r, http.StatusConflict, "conflict",
				"run "+runID+" is parked: decide its pending calls (approve/deny) instead")
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
