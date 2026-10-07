package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"

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
	mux.HandleFunc("POST /api/runs/{id}/approvals", s.servePlaygroundApproval(rs))
	mux.HandleFunc("POST /api/playground/fixtures", s.servePlaygroundFixture)
	mux.HandleFunc("GET /api/experiments", s.serveExperiments)
	mux.HandleFunc("POST /api/experiments", s.serveExperiments)
	mux.HandleFunc("GET /api/experiments/{id}", s.serveExperiment)
	// The debugger's rungs 3–4 (WEFT-DEVTOOLS §8.3/§8.4), each its own
	// capability: the panel and the Studio UI render a control only
	// when meta reports it (§8.5 item 3).
	mux.HandleFunc("PUT /api/runtimes/{id}/breakpoints", s.serveBreakpoints(rs))
	mux.HandleFunc("POST /api/runs/{id}/steer", s.serveSteer(rs))
	// The runtime link's own block (§10.3), as its own capability —
	// mounted behind the server-identity guard: the link speaks
	// server-to-server (register overwrites a runtime's registration,
	// the commands stream replaces its feed, acks forge the state the
	// steer/approval routing trusts), so a panel token that passes the
	// wall must still be refused here (S4.6).
	s.addGroup(routeGroup{
		name:       "runtimes",
		capability: "runtimes",
		register: func(mux *http.ServeMux, s *Server) {
			rs.MountGuarded(mux, s.serverOnly)
		},
	})
	s.addGroup(routeGroup{
		name:       "breakpoints",
		capability: "breakpoints",
		register:   func(mux *http.ServeMux, s *Server) { /* mounted above */ },
	})
	s.addGroup(routeGroup{
		name:       "steer",
		capability: "steer",
		register:   func(mux *http.ServeMux, s *Server) { /* mounted above */ },
	})
}

// ── GET /api/runtimes ─────────────────────────────────────────────

// serveRuntimes answers §10.4's connected-runtimes view: every
// runtime that registered, with liveness and its agents. Open to any
// authenticated reader (a panel's read token included): this is the
// picker's data, not per-public-id content — except each agent's
// registered system prompt, which only an identity that may start
// experiments needs (the drawer pre-fills from it). A read-scoped
// panel token lives in a page that only views: it gets the picker
// without the prompts.
func (s *Server) serveRuntimes(rs *linkruntime.RuntimeServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		views := rs.Snapshot()
		if p := idFrom(r).panel; p != nil && p.Scope != scopePlayground {
			for i := range views {
				for j := range views[i].Agents {
					views[i].Agents[j].Instructions = ""
				}
			}
		}
		writeJSON(w, r, http.StatusOK, struct {
			Runtimes []linkruntime.RuntimeView `json:"runtimes"`
		}{views})
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
		if !decodeBody(w, r, "run body", &req) {
			return
		}

		// A panel token may act inside its public id only, and a
		// read-scoped one may not act at all (S4.6) — checked before
		// anything below reads the database, so no validation message
		// can describe another public id's run. The source run is
		// inside the scope too: the new run would carry the token's
		// public id and the source's whole transcript with it.
		if id := idFrom(r); id.panel != nil {
			if !mayAct(w, r) {
				return
			}
			if req.PublicID != id.panel.PublicID {
				forbidden(w, r)
				return
			}
			// Experiments are the server token's (experiments.go): an id
			// here would file the page's run under the operator's
			// experiment and spend its runtime budget (capped per id).
			if req.ExperimentID != "" {
				writeError(w, r, http.StatusForbidden, "forbidden",
					"experiment_id is the server token's: a panel token runs outside experiments")
				return
			}
			if req.Source != nil && req.Source.RunID != "" && !s.scopeRunID(w, r, req.Source.RunID) {
				return
			}
		}
		// The caller's command id is written as the SSE frame id on the
		// runtime's stream: anything that could break a line there is
		// refused (linkruntime.ValidCommandID).
		if req.CommandID != "" && !linkruntime.ValidCommandID(req.CommandID) {
			badRequest(w, r, "command_id must be 1 to 128 characters of [A-Za-z0-9._:-]")
			return
		}

		// The transcript edits (D2/D3) validate against the source
		// transcript: the boundary rule and the no-call-without-result
		// rule are 400s (§10.4), so Repair never synthesizes what
		// nobody wrote.
		if len(req.TranscriptEdits) > 0 {
			switch {
			case req.Source == nil || req.Source.RunID == "":
				badRequest(w, r, "transcript_edits need a source run")
				return
			case req.Source.FromStep <= 0:
				badRequest(w, r, "transcript_edits need from_step > 0 (0 re-runs the whole turn, nothing is kept)")
				return
			}
			batches, terr := s.db.TranscriptBatches(r.Context(), req.Source.RunID)
			if terr != nil {
				badRequest(w, r, "the source run has no readable transcript to edit")
				return
			}
			// The run's own steps, each where its record says it joined
			// (the input record is context, never a step).
			steps, serr := runSteps(batches)
			if serr != nil {
				badRequest(w, r, "the source run has no readable transcript to edit")
				return
			}
			if verr := validateTranscriptEdits(steps, req.Source.FromStep, req.TranscriptEdits); verr != nil {
				badRequest(w, r, verr.Error())
				return
			}
		}
		switch req.Engine {
		case "", "live":
		case "scripted":
			// §5.5's prompt trap: the replay key ignores the system
			// prompt by design, so an instructions or model override
			// would silently replay the old answer — refused. Changes
			// that alter the key miss instead and fail the step with
			// "no recorded turn".
			if req.Overrides.Instructions != "" {
				badRequest(w, r, "scripted engine with an instructions override would silently replay the old answer (WEFT-PLAYGROUND §5.5)")
				return
			}
			if req.Overrides.Model != "" {
				badRequest(w, r, "scripted engine with a model override would silently replay the old answer (WEFT-PLAYGROUND §5.5)")
				return
			}
			if req.Source == nil || req.Source.RunID == "" {
				badRequest(w, r, "the scripted engine replays a source run's recorded turns: a source run is required")
				return
			}
		default:
			badRequest(w, r, "unknown engine "+req.Engine)
			return
		}
		switch req.Thread {
		case "", "ephemeral":
		case "fork":
			// §5.4: fork continues the conversation in a new session
			// with lineage — it needs a source turn and an input;
			// from_step is the ephemeral verb (a mid-turn re-run),
			// never the fork's. The runtime's own threads availability
			// is checked against its registration below.
			if req.Source == nil || req.Source.RunID == "" {
				badRequest(w, r, "fork mode forks a source turn's session: a source run is required")
				return
			}
			if req.Input == nil || *req.Input == "" {
				badRequest(w, r, "fork mode continues the conversation: an input is required")
				return
			}
			if req.Source.FromStep > 0 {
				badRequest(w, r, "fork mode re-runs no steps (from_step is the ephemeral verb); send an input instead")
				return
			}
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
		if req.Thread == "fork" && !reg.Threads {
			badRequest(w, r, "fork mode needs runtime.Threads(store) on this runtime")
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
		for key, v := range req.Overrides.Options {
			switch key {
			case "max_steps", "parallelism":
				// A whole number from 1: the lower-only checks below
				// compare it as an int, and a negative, fractional or
				// out-of-range value would slip under them.
				if v < 1 || v > 1e6 || v != math.Trunc(v) {
					badRequest(w, r, key+" must be a whole number from 1")
					return
				}
			case "temperature":
				// The range every provider accepts — the runtime's own
				// check (validOptions), answered here as a 400 instead of
				// a rejected command.
				if v < 0 || v > 2 {
					badRequest(w, r, "temperature must be between 0 and 2")
					return
				}
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
			// "allow" runs the AllowSideEffects tools for real; a tool
			// the code vouched ReplaySafe (or an Output agent's
			// submission) is no side effect and runs in every mode, so it
			// may stay on too — the runtime's own check is the same.
			for _, name := range touched {
				if !agent.IsAllowed(name) && agent.SideEffects[name] != "safe" && name != "submit_output" {
					writeError(w, r, http.StatusForbidden, "forbidden",
						"tool "+name+" is not opted in for real side effects")
					return
				}
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
			case errors.Is(err, linkruntime.ErrInvalidCommandID):
				badRequest(w, r, "command_id must be 1 to 128 characters of [A-Za-z0-9._:-]")
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

// ── POST /api/runs/{id}/approvals ─────────────────────────────────

// approvalRequest is the decision body: continue (approve — the
// handler runs for real), skip (deny with a reason), or resolve (a
// result pasted from outside the process). These are the approval
// verbs of a parked experiment run (WEFT-DEVTOOLS §8.2, ADR 0007),
// forwarded to the runtime that started the run.
type approvalRequest struct {
	CallID   string `json:"call_id"`
	Decision string `json:"decision"` // approve | deny | resolve
	Reason   string `json:"reason"`
	Content  string `json:"content"`
}

// servePlaygroundApproval forwards one decision on one parked call of
// a runtime-started run (P1's approval controls; WEFT-DEVTOOLS §8.2).
// The app's own turns are viewer-only (D7, PQ7): a run no runtime
// started is refused here, and meta/both UIs say so.
func (s *Server) servePlaygroundApproval(rs *linkruntime.RuntimeServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runID := r.PathValue("id")
		var req approvalRequest
		if !decodeBody(w, r, "approval body", &req) {
			return
		}
		// A decision is a write verb: the handler may run for real. A
		// read-scoped panel token does not decide (S4.6).
		if !mayAct(w, r) {
			return
		}
		switch req.Decision {
		case "approve", "deny", "resolve":
		default:
			badRequest(w, r, "unknown decision "+req.Decision)
			return
		}
		if req.CallID == "" {
			badRequest(w, r, "an approval needs a call_id")
			return
		}
		row, dbErr := s.db.Run(r.Context(), runID)
		runtimeID, started := rs.RuntimeOf(runID)
		if !started {
			// Not a runtime-started run. An unknown id is a 404; the
			// app's own turns exist and stay viewer-only (D7, PQ7).
			if dbErr != nil {
				if errors.Is(dbErr, obsdb.ErrNotFound) {
					notFound(w, r, "unknown run "+runID)
					return
				}
				dbError(w, r, "run", runID, dbErr)
				return
			}
			writeError(w, r, http.StatusForbidden, "forbidden",
				"only runs a runtime started can be decided — the app's own turns are viewer-only (PQ7)")
			return
		}
		// A panel token decides inside its public id only (S4.6): the
		// scope the command carried, or the row's when it has landed.
		// The decision's own command carries the same scope — the page
		// polls it, and its acks name the resumed run.
		publicID, _ := rs.PublicOf(runID)
		if publicID == "" && dbErr == nil {
			publicID = row.PublicID
		}
		if pid := idFrom(r).panel; pid != nil && publicID != pid.PublicID {
			forbidden(w, r)
			return
		}
		// The call must be one the parked run is waiting on (the
		// audit's P2-16): the core resumes with exactly the decisions it
		// is handed and answers every other pending call "DENIED: no
		// decision", so a stale or mistyped id would silently deny the
		// call the human meant to decide. The run's own run_finish names
		// the pending set; until it has landed there is nothing to check
		// against and the runtime's own check is the only one.
		if dbErr == nil {
			pending, known, err := s.pendingCalls(r.Context(), runID)
			if err != nil {
				dbError(w, r, "events of run", runID, err)
				return
			}
			if known && !contains(pending, req.CallID) {
				if len(pending) == 0 {
					badRequest(w, r, "run "+runID+" is not parked: it finished with no pending call")
					return
				}
				badRequest(w, r, "call "+req.CallID+" is not pending on run "+runID+
					" (pending: "+strings.Join(pending, ", ")+")")
				return
			}
		}
		if !rs.Connected(runtimeID) {
			writeError(w, r, http.StatusServiceUnavailable, "unavailable",
				"runtime "+runtimeID+" is not connected")
			return
		}
		d, err := rs.EnqueueApproval(runtimeID, linkruntime.ApprovalDecision{
			RunID:    runID,
			CallID:   req.CallID,
			Decision: req.Decision,
			Reason:   req.Reason,
			Content:  req.Content,
			Actor:    actorOf(r),
			PublicID: publicID,
		})
		if err != nil {
			switch {
			case errors.Is(err, linkruntime.ErrUnknownRuntime):
				notFound(w, r, "unknown runtime "+runtimeID)
			case errors.Is(err, linkruntime.ErrNotConnected):
				writeError(w, r, http.StatusServiceUnavailable, "unavailable",
					"runtime "+runtimeID+" is not connected")
			case errors.Is(err, linkruntime.ErrDuplicateCommand):
				writeError(w, r, http.StatusConflict, "conflict",
					"command id "+d.CommandID+" was already used")
			default:
				writeError(w, r, http.StatusInternalServerError, "internal", err.Error())
			}
			return
		}
		writeJSON(w, r, http.StatusAccepted, struct {
			CommandID string `json:"command_id"`
			State     string `json:"state"`
		}{d.CommandID, linkruntime.StateQueued})
	}
}

// pendingCalls reads the call ids a run parked on from its stored
// run_finish event (RunFinish.Pending — the ids survive content-off
// capture; only the args are stripped). known is false while no
// run_finish has landed: the run is still running, or its records are
// in flight.
func (s *Server) pendingCalls(ctx context.Context, runID string) (ids []string, known bool, err error) {
	after := int64(-1)
	for {
		page, err := s.db.Events(ctx, runID, after, 1000)
		if err != nil {
			if errors.Is(err, obsdb.ErrNotFound) {
				return nil, false, nil
			}
			return nil, false, err
		}
		for _, pe := range page.Events {
			if !bytes.Contains(pe.Event, []byte(`"run_finish"`)) {
				continue
			}
			var ev struct {
				Type    string `json:"type"`
				Pending []struct {
					ID string `json:"id"`
				} `json:"pending"`
			}
			if json.Unmarshal(pe.Event, &ev) != nil || ev.Type != "run_finish" {
				continue
			}
			ids, known = ids[:0], true
			for _, p := range ev.Pending {
				ids = append(ids, p.ID)
			}
		}
		if page.NextAfter == nil || len(page.Events) == 0 {
			return ids, known, nil
		}
		after = *page.NextAfter
	}
}
