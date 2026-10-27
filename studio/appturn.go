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
	"time"

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
func (s *Server) appTurnRuntime(w http.ResponseWriter, r *http.Request, rs *linkruntime.RuntimeServer, row obsdb.RunRow) (string, bool) {
	runtimeID, ok := rs.SessionsRuntime(row.Service)
	if !ok {
		msg, fix := s.appRefusal(r, rs, row)
		refuseAppOwned(w, r, msg, fix)
		return "", false
	}
	if row.SessionID == "" || row.PublicID == "" {
		// A plain Generate's park (no session) is the app's to resume;
		// a session turn without a public id needs one (UX-D-17).
		fix := "run the agent through weft/thread with thread.PublicID(...): runtime.Sessions resolves a session by its public id"
		if row.SessionID == "" {
			fix = "resume it in your app; to decide here, run it as a session turn (weft/thread)"
		}
		refuseAppOwned(w, r,
			appOwned+" · read-only: run "+row.ID+" is not a turn of a session with a public id", fix)
		return "", false
	}
	return runtimeID, true
}

// appRefusal is the 403's message and fix when no connected runtime of
// the run's service registered runtime.Sessions — the four cases the
// UIs tell apart in the same words (lib/honesty.ts refusalWords):
//
//   - a runtime of the service that registered Sessions is listed
//     disconnected (U20.2, UX-H-14): the app was opted in and went away
//     — restart it, never "add runtime.Sessions", even while another
//     process of the service is connected without Sessions;
//   - one of the service is connected without Sessions: set it;
//   - none of the service is known here, and this process's own link
//     dialled another Studio (meta's runtime_link, UX-D-01): name it;
//   - otherwise the link is closed: open it.
func (s *Server) appRefusal(r *http.Request, rs *linkruntime.RuntimeServer, row obsdb.RunRow) (msg, fix string) {
	service := row.Service
	if service == "" {
		service = "(none recorded)"
	}
	// The opted-in app gone wins over another process of the service
	// connected without Sessions (a CLI beside a crashed app): the app's
	// fix is its restart, never "set runtime.Sessions" (UX-H-14).
	if gone, ok := goneSessionsRuntime(rs, row.Service); ok {
		return appGoneWords(gone)
	}
	if serviceConnected(rs, row.Service) {
		return appOwned + " · read-only: run " + row.ID + " is the app's own, and no connected runtime of its service " + service + " acts on the app's sessions",
			"set runtime.Sessions(resolver) at runtime.Install in the app (service " + service + "): its turns then take approvals and steer"
	}
	if mayReadDBPath(s, r) {
		if l := s.runtimeLink(); l != nil && l.StudioURL != "" && !l.InProcess {
			return appOwned + " · read-only: no runtime of service " + service + " is connected here — the app's link dialled " + l.StudioURL,
				appElsewhereFix
		}
	}
	return appOwned + " · read-only: no runtime of service " + service + " is connected",
		"open the runtime link in the app: WEFT_ENV=dev (or runtime.Enabled(true)) at runtime.Install, with runtime.Sessions(resolver)"
}

// appElsewhereFix is the fix when the app's link dialled another Studio.
const appElsewhereFix = "open the panel there (data-endpoint), or link in-process: runtime.Local(srv)"

// goneSessionsRuntime is the newest-seen runtime of service that
// registered runtime.Sessions and holds no stream now: the app opted in
// and went away (it stopped, crashed, or Studio restarted under it).
func goneSessionsRuntime(rs *linkruntime.RuntimeServer, service string) (linkruntime.RuntimeView, bool) {
	if service == "" {
		return linkruntime.RuntimeView{}, false
	}
	for _, v := range rs.Snapshot() { // newest seen first
		if !v.Connected && v.Sessions && v.Service == service {
			return v, true
		}
	}
	return linkruntime.RuntimeView{}, false
}

// appGoneWords is the refusal for an opted-in app whose runtime is
// gone, in lib/honesty.ts's words (appGone): disconnected — the app
// stopped, or Studio restarted under it and it is not back yet (the
// runtimes view says which) — last seen to the second, in UTC.
func appGoneWords(v linkruntime.RuntimeView) (msg, fix string) {
	return appOwned + " · read-only: the app's runtime " + v.ID + " of service " + v.Service +
			" disconnected — last seen " + v.LastSeen.UTC().Format(time.RFC3339),
		"restart the app: its runtime link registers again"
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
