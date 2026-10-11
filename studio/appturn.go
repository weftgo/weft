package studio

// The debugger's verbs on the app's own turns (plan F4.1): approvals,
// steer and cancel on a run no runtime started. Studio never runs the
// app's agent and never writes its session; it forwards the verb as a
// `session` command to a connected runtime of the run's service that
// registered runtime.Sessions — the app's opt-in — and the runtime acts
// through the app's own Session, under its writer lease. Without such a
// runtime the three verbs answer alike: 403 "app-owned turn", with the
// one-line fix.
//
// The route answers 202 with the command id as soon as the command is
// on the runtime's stream, never waiting for its ack: the runtime's
// refusal — "the app is writing this session; fork instead" above all —
// arrives on the command's row (GET /api/playground/commands/{id}:
// state rejected, error the sentence), which the client polls as it
// polls every playground command.

import (
	"errors"
	"net/http"

	"github.com/weftgo/weft/obsdb"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// appOwned is the refusal's message head: the panel and the Studio UI
// key the read-only state on it.
const appOwned = "app-owned turn"

// appTurnResponse is the 202's body for a verb on an app-owned turn:
// the command to poll (the approvals route's shape), the run it acts on
// and the runtime it went to.
type appTurnResponse struct {
	CommandID string `json:"command_id"`
	State     string `json:"state"`
	RunID     string `json:"run_id"`
	RuntimeID string `json:"runtime_id"`
}

// refuseAppOwned is the 403 an app-owned turn answers when nothing may
// act on it: the error shape every route uses, with fix beside it — what
// the app adds so the verb works.
func refuseAppOwned(w http.ResponseWriter, r *http.Request, msg, fix string) {
	type errBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	writeJSON(w, r, http.StatusForbidden, struct {
		Error errBody `json:"error"`
		Fix   string  `json:"fix"`
	}{errBody{"forbidden", msg}, fix})
}

// appTurnRuntime names the runtime an app-owned run is acted on
// through: a connected runtime of the run's service that registered
// runtime.Sessions, the run a turn of a thread session with a public id
// (what the app's resolver is asked by). Otherwise it answers the 403
// and reports false. The caller has already checked the identity may
// act and the run is inside its scope.
func appTurnRuntime(w http.ResponseWriter, r *http.Request, rs *linkruntime.RuntimeServer, row obsdb.RunRow) (string, bool) {
	runtimeID, ok := rs.SessionsRuntime(row.Service)
	if !ok {
		service := row.Service
		if service == "" {
			service = "(none recorded)"
		}
		// No runtime of the service connected at all — the app's link is
		// closed (no WEFT_ENV=dev, no runtime.Enabled(true)) or it is not
		// running — is told apart from one connected without Sessions:
		// the fix differs.
		if !serviceConnected(rs, row.Service) {
			refuseAppOwned(w, r,
				appOwned+" · read-only: no runtime of service "+service+" is connected",
				"open the runtime link in the app: WEFT_ENV=dev (or runtime.Enabled(true)) at runtime.Install, with runtime.Sessions(resolver)")
			return "", false
		}
		refuseAppOwned(w, r,
			appOwned+" · read-only: run "+row.ID+" is the app's own, and no connected runtime of its service "+service+" acts on the app's sessions",
			"set runtime.Sessions(resolver) at runtime.Install in the app (service "+service+"): its turns then take approvals and steer")
		return "", false
	}
	if row.SessionID == "" || row.PublicID == "" {
		refuseAppOwned(w, r,
			appOwned+" · read-only: run "+row.ID+" is not a turn of a thread session with a public id",
			"run the agent through weft/thread with thread.PublicID(...): runtime.Sessions resolves a session by its public id")
		return "", false
	}
	return runtimeID, true
}

// serviceConnected reports whether any runtime of service holds its
// command stream now (Sessions or not). A run with no service recorded
// has none.
func serviceConnected(rs *linkruntime.RuntimeServer, service string) bool {
	if service == "" {
		return false
	}
	for _, v := range rs.Snapshot() {
		if v.Connected && v.Service == service {
			return true
		}
	}
	return false
}

// enqueueAppTurn hands the verb to the runtime and answers 202 with the
// command, or the enqueue's error (the approvals route's mapping).
func enqueueAppTurn(w http.ResponseWriter, r *http.Request, rs *linkruntime.RuntimeServer, runtimeID string, sc linkruntime.SessionCommand) {
	sc, err := rs.EnqueueSession(runtimeID, sc)
	if err != nil {
		switch {
		case errors.Is(err, linkruntime.ErrUnknownRuntime):
			notFound(w, r, "unknown runtime "+runtimeID)
		case errors.Is(err, linkruntime.ErrNotConnected):
			writeError(w, r, http.StatusServiceUnavailable, "unavailable",
				"runtime "+runtimeID+" is not connected")
		case errors.Is(err, linkruntime.ErrDuplicateCommand):
			writeError(w, r, http.StatusConflict, "conflict",
				"command id "+sc.CommandID+" was already used")
		default:
			writeError(w, r, http.StatusInternalServerError, "internal", err.Error())
		}
		return
	}
	writeJSON(w, r, http.StatusAccepted, appTurnResponse{
		CommandID: sc.CommandID, State: linkruntime.StateQueued, RunID: sc.RunID, RuntimeID: runtimeID,
	})
}

// appSessionCommand is the frame's addressing for one app-owned run.
func appSessionCommand(r *http.Request, verb string, row obsdb.RunRow) linkruntime.SessionCommand {
	return linkruntime.SessionCommand{
		Verb:      verb,
		PublicID:  row.PublicID,
		SessionID: row.SessionID,
		RunID:     row.ID,
		Actor:     actorOf(r),
	}
}
