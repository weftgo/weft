package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// The command's wire schema carries transcript edits, the scripted
// engine and fork mode; the handler validates each (§10.4) before the
// command reaches a runtime.

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
// for it — returns it. Over a DB that implements obsdb.CommandStore
// (both first-party backends do) the link server writes its commands,
// routes, breakpoint sets and registrations through and loads them
// back after a restart (plan I3); over one that does not it keeps them
// in memory, as before.
func registerPlayground(mux *http.ServeMux, s *Server) {
	var opts []linkruntime.Option
	if cs, ok := s.db.(obsdb.CommandStore); ok {
		opts = append(opts, linkruntime.WithStore(cs))
	}
	rs := linkruntime.New(opts...)
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
	// The preview (plan F2): pure assembly of a replay's first request,
	// no runtime needed — its own capability so the UIs gate the
	// "will be sent" pane on it.
	s.addGroup(routeGroup{
		name:       "preview",
		capability: "preview",
		register: func(mux *http.ServeMux, s *Server) {
			mux.HandleFunc("POST /api/playground/preview", s.servePlaygroundPreview(rs))
		},
	})
	// The target facts (U18.1): GET /api/runtimes' per-agent
	// manifest_hash and studio_url, the preview's scripted_hit /
	// scripted_key_match, POST /api/playground/runs' cross-agent
	// refusal (cross_agent: true opts in) and a command's reason. No
	// route of its own: the capability is what a client gates its
	// target check on.
	s.addGroup(routeGroup{name: "target_check", capability: "target_check"})
	// Forget (U20.1): a runtime that is gone — disconnected, restored
	// after a restart and never back — leaves the list and the command
	// store; its own capability so the /runtimes page draws the verb
	// only when meta reports it.
	s.addGroup(routeGroup{
		name:       "runtime_forget",
		capability: "runtime_forget",
		register: func(mux *http.ServeMux, s *Server) {
			mux.HandleFunc("DELETE /api/runtimes/{id}", s.serveForgetRuntime(rs))
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
	// The Live page's cancel (plan H6, cancel.go): a runtime-started
	// run, stopped through the link's cancel frame — its own capability
	// so the UIs draw the button only when meta reports it.
	s.addGroup(routeGroup{
		name:       "cancel",
		capability: "cancel",
		register: func(mux *http.ServeMux, s *Server) {
			mux.HandleFunc("POST /api/runs/{id}/cancel", s.serveCancel(rs))
		},
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
// without the prompts. Each row's cwd follows db.path's rule.
func (s *Server) serveRuntimes(rs *linkruntime.RuntimeServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		views := rs.Snapshot()
		if !mayReadDBPath(s, r) {
			// The app's working directory is a filesystem path: db.path's
			// rule (the server token, or setup A's loopback), never a
			// panel token's.
			// The Studio URL the runtime dialled is an address on the
			// operator's network: the same rule.
			for i := range views {
				views[i].Cwd, views[i].StudioURL = "", ""
			}
		}
		if !readsPrompts(r) {
			// A read-scoped panel token reads no prompt-adjacent value:
			// the system prompt, a named default tool choice's tool (the
			// spans' rule, toolNameAttrs) and the stop sequences. The
			// defaults are copied: the view shares the registration's.
			for i := range views {
				for j := range views[i].Agents {
					a := &views[i].Agents[j]
					a.Instructions = ""
					if a.Defaults != nil {
						d := *a.Defaults
						d.ToolChoice.Name, d.Stop = "", nil
						a.Defaults = &d
					}
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
	// CrossAgent is the caller's explicit opt-in to replaying a source
	// run as another agent than the one that recorded it (UX-C-03):
	// without it, POST /api/playground/runs refuses the mismatch.
	// Studio's alone — it never reaches the runtime.
	CrossAgent bool `json:"cross_agent,omitempty"`
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
		// A source derived from stock spans has nothing to replay: 400
		// with the derived badge. After the panel token's rules (who may
		// ask), before every check of the command's content — its ids
		// included — the preview's position, so both routes answer one
		// body alike.
		if req.Source != nil && s.refuseDerivedSource(w, r, req.Source.RunID) {
			return
		}
		// The caller's command id is written as the SSE frame id on the
		// runtime's stream: anything that could break a line there is
		// refused (linkruntime.ValidCommandID).
		if req.CommandID != "" && !linkruntime.ValidCommandID(req.CommandID) {
			badRequest(w, r, "command_id must be 1 to 128 characters of [A-Za-z0-9._:-]")
			return
		}
		// The experiment id is the command id's rule (experiments.go's
		// POST holds a saved definition's id to it too): it labels the
		// run, keys the runtime's budget and is a URL path segment.
		if req.ExperimentID != "" && !linkruntime.ValidCommandID(req.ExperimentID) {
			badRequest(w, r, "experiment_id must be 1 to 128 characters of [A-Za-z0-9._:-]")
			return
		}

		// The registered agent when the runtime holds it: its manifest's
		// schemas are what a tool_args edit is checked against (the
		// runtime's own). Its absence is refused below, in order.
		var registered *linkruntime.AgentRegistration
		if reg, ok := rs.Registration(req.Runtime); ok && req.Runtime != "" {
			if a, ok := reg.Agent(req.Agent); ok {
				registered = &a
			}
		}
		if e := s.checkCommand(r.Context(), &req, false, registered); e != nil {
			e.write(w, r)
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
		if e := s.checkTargetNamed(r.Context(), &req); e != nil {
			e.write(w, r)
			return
		}
		agent, ok := reg.Agent(req.Agent)
		if !ok {
			notFound(w, r, "unknown agent "+req.Agent)
			return
		}

		if e := s.checkSource(r.Context(), &req); e != nil {
			e.write(w, r)
			return
		}
		if e := s.checkTarget(r.Context(), &req); e != nil {
			e.write(w, r)
			return
		}

		if e := s.checkSkew(r.Context(), &req, agent); e != nil {
			e.write(w, r)
			return
		}
		if e := checkRegistered(&req, agent); e != nil {
			e.write(w, r)
			return
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

// cmdError is one refusal of a §5.1 command: its status, error code
// and sentence — what POST /api/playground/runs and the preview both
// answer it with (§10.4's table).
type cmdError struct {
	status    int
	code, msg string
}

func (e *cmdError) write(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, e.status, e.code, e.msg)
}

// refuse is a 400; refuseWidening the 403 a widening override gets.
func refuse(msg string) *cmdError { return &cmdError{http.StatusBadRequest, "bad_request", msg} }

func refuseWidening(msg string) *cmdError {
	return &cmdError{http.StatusForbidden, "forbidden", msg}
}

// checkCommand runs the checks a command needs no runtime for: the
// transcript edits against the source run's records (D2/D3 — the
// boundary rule and the no-call-without-result rule are 400s, §10.4,
// so Repair never synthesizes what nobody wrote), the engine, the
// thread mode and the side-effect mode. preview is the preview's
// reading of the scripted engine's refusals (§5.5's prompt trap, the
// step it never answered, the edits it cannot answer): returned as
// warnings instead, worded the same, never a 400 there.
func (s *Server) checkCommand(ctx context.Context, req *runRequest, preview bool, agent *linkruntime.AgentRegistration) *cmdError {
	_, err := s.checkCommandWarn(ctx, req, preview, true, agent)
	return err
}

// checkCommandWarn is checkCommand with the scripted warnings the
// preview reads (always nil when preview is false). prompts is
// readsPrompts' answer for the identity: without it the tool schemas
// stay hidden — a tool_args edit is checked for its object shape
// alone, so no refusal names what the catalog declares. agent is the
// registered agent when the body's runtime holds it (nil: none), whose
// manifest schemas a tool_args edit is checked against (editSchemas).
func (s *Server) checkCommandWarn(ctx context.Context, req *runRequest, preview, prompts bool, agent *linkruntime.AgentRegistration) ([]string, *cmdError) {
	if len(req.TranscriptEdits) > 0 {
		switch {
		case req.Source == nil || req.Source.RunID == "":
			return nil, refuse("transcript_edits need a source run")
		case req.Source.FromStep <= 0:
			return nil, refuse("transcript_edits need from_step > 0 (0 re-runs the whole turn, nothing is kept)")
		}
		batches, terr := s.db.TranscriptBatches(ctx, req.Source.RunID)
		if terr != nil {
			return nil, refuse("the source run has no readable transcript to edit")
		}
		// The run's input (a user edit may rewrite the turn's prompt)
		// and its own steps, each where its record says it joined.
		input, ierr := runInput(batches)
		steps, serr := runSteps(batches)
		if ierr != nil || serr != nil {
			return nil, refuse("the source run has no readable transcript to edit")
		}
		// The replay prefix is what the model saw at from_step (ADR
		// 0029): when that request carried a compaction view, an
		// edit inside its range is refused. A step the run never
		// reached has no view; the step-count rule refuses it.
		var view *compactedRange
		sm, aerr := obsdb.MessagesAsOf(ctx, s.db, req.Source.RunID, req.Source.FromStep)
		switch {
		case aerr == nil:
			view = compactedRangeOf(batches, sm)
		case !errors.Is(aerr, obsdb.ErrNotFound):
			return nil, refuse(fmt.Sprintf("the source run's step %d does not rebuild from its records: %v", req.Source.FromStep, aerr))
		}
		var schemas schemaOf
		if prompts {
			schemas = s.editSchemas(ctx, req.Source.RunID, agent)
		}
		if verr := validateTranscriptEdits(input, steps, req.Source.FromStep, req.TranscriptEdits, view, schemas); verr != nil {
			return nil, refuse(verr.Error())
		}
	}
	var warnings []string
	trap := func(msg string) *cmdError {
		if preview {
			warnings = append(warnings, msg)
			return nil
		}
		return refuse(msg)
	}
	switch req.Engine {
	case "", "live":
	case "scripted":
		// §5.5's prompt trap: the replay key ignores the system
		// prompt by design, so an instructions or model override
		// would silently replay the old answer — refused. Changes
		// that alter the key miss instead and fail the step with
		// "no recorded turn" — except a transcript edit, refused here
		// in weft/runtime's words (executor.go's scriptedEdits).
		if req.Overrides.Instructions != "" {
			if e := trap("scripted cannot try a changed system prompt: the recorded answers ignore the prompt, so it would replay the old answer — run on live, or drop the prompt change"); e != nil {
				return nil, e
			}
		}
		if req.Overrides.Model != "" {
			if e := trap("scripted cannot try another model: it replays the answers the source's own model recorded — run on live, or drop the model override"); e != nil {
				return nil, e
			}
		}
		if req.Source == nil || req.Source.RunID == "" {
			return nil, refuse("scripted replays a recorded run's answers: pick a source run, or run on live")
		}
		if len(req.TranscriptEdits) > 0 {
			if e := trap(fmt.Sprintf("your edit changes step %d's request, and scripted holds only the recorded answer to the original — run on live, or drop the edit", req.Source.FromStep)); e != nil {
				return nil, e
			}
		}
		// from_step at the step count is the step the source never
		// answered (ADR 0029): nothing recorded to replay — refused in
		// weft/runtime's words (executor.go's scriptedAtCount). A
		// transcript that does not read leaves it to the runtime.
		if n := req.Source.FromStep; n > 0 {
			if batches, terr := s.db.TranscriptBatches(ctx, req.Source.RunID); terr == nil {
				if steps, serr := runSteps(batches); serr == nil && stepCount(steps) == n {
					if e := trap(fmt.Sprintf("scripted has no recorded answer for step %d: the source never answered it — run on live, or replay from an earlier step", n)); e != nil {
						return nil, e
					}
				}
			}
		}
	default:
		return nil, refuse("unknown engine " + req.Engine)
	}
	switch req.Thread {
	case "", "ephemeral":
	case "fork":
		// §5.4: fork continues the conversation in a new session
		// with lineage — it needs a source turn and an input;
		// from_step is the ephemeral verb (a mid-turn re-run),
		// never the fork's. The runtime's own threads availability
		// is checked against its registration.
		if req.Source == nil || req.Source.RunID == "" {
			return nil, refuse("fork mode forks a source turn's session: a source run is required")
		}
		if req.Input == nil || *req.Input == "" {
			return nil, refuse("fork mode continues the conversation: an input is required")
		}
		if req.Source.FromStep > 0 {
			return nil, refuse("fork mode re-runs no steps (from_step is the ephemeral verb); send an input instead")
		}
	default:
		return nil, refuse("unknown thread mode " + req.Thread)
	}
	switch req.SideEffects {
	case "", "substitute", "park", "allow":
	default:
		return nil, refuse("unknown side_effects mode " + req.SideEffects)
	}
	return warnings, nil
}

// editSchemas resolves the input schema a tool_args edit is checked
// against on this side. With the agent registered (agent non-nil and
// its manifest readable) it is the manifest's — what the runtime itself
// checks against, its agent's current tools; a tool the manifest lists
// without a schema, or does not list, needs only an object there too.
// Without, it is the recorded catalog of the step whose call the edit
// rewrites (that step's answering request: the schema the model wrote
// the call against); a step with no request record, or whose catalog
// lacks the tool, falls back to the run's latest recorded schema for the
// name. None recorded (a run before ADR 0028, a content-off chain): the
// arguments need only be an object. The records are read once, on the
// first lookup.
func (s *Server) editSchemas(ctx context.Context, runID string, agent *linkruntime.AgentRegistration) schemaOf {
	if agent != nil {
		if m, ok := agent.ManifestToolSchemas(); ok {
			return func(_ int, name string) json.RawMessage { return m[name] }
		}
	}
	var (
		loaded bool
		latest map[string]json.RawMessage         // by name, the run's last catalog winning
		byStep map[int]map[string]json.RawMessage // step → its answering request's catalog
	)
	load := func() {
		loaded = true
		latest, byStep = map[string]json.RawMessage{}, map[int]map[string]json.RawMessage{}
		cats, err := s.db.Catalogs(ctx, runID)
		if err != nil {
			return
		}
		byHash := map[string]map[string]json.RawMessage{}
		for _, c := range cats {
			m := map[string]json.RawMessage{}
			for _, t := range c.Tools {
				if len(t.Schema) > 0 {
					m[t.Name] = t.Schema
					latest[t.Name] = t.Schema
				}
			}
			byHash[c.Hash] = m
		}
		// Each step's answering attempt (the highest index) names its
		// catalog; paged to the end.
		last := map[int]obsdb.RequestRecord{}
		q := obsdb.RequestQuery{Limit: maxRequestsLimit}
		for {
			page, err := s.db.Requests(ctx, runID, q)
			if err != nil {
				break
			}
			for _, r := range page {
				if cur, ok := last[r.Step]; !ok || r.Index > cur.Index {
					last[r.Step] = r
				}
			}
			if len(page) < q.PageLimit() {
				break
			}
			q.From = page[len(page)-1].Index + 1
		}
		for step, r := range last {
			if m, ok := byHash[r.CatalogHash]; ok && r.CatalogHash != "" {
				byStep[step] = m
			}
		}
	}
	return func(step int, name string) json.RawMessage {
		if !loaded {
			load()
		}
		if sch, ok := byStep[step][name]; ok {
			return sch
		}
		return latest[name]
	}
}

// checkSource checks the source turn: from_step is 0+ and input may not
// replace a continued turn's message (both 400, body-shape errors), and
// the run must exist (404). A run derived from stock spans was refused
// before it (refuseDerivedSource, the preview's check at the preview's
// position).
func (s *Server) checkSource(ctx context.Context, req *runRequest) *cmdError {
	if req.Source == nil || req.Source.RunID == "" {
		return nil
	}
	if req.Source.FromStep < 0 {
		return refuse("source.from_step must be 0 or more")
	}
	if req.Input != nil && *req.Input != "" && req.Source.FromStep > 0 {
		return refuse(`input replaces the turn's user message only when from_step is 0: with from_step > 0, edit step 0's user message instead (a transcript edit of kind "user")`)
	}
	if _, err := s.db.Run(ctx, req.Source.RunID); err != nil {
		if errors.Is(err, obsdb.ErrNotFound) {
			return &cmdError{http.StatusNotFound, "not_found", "unknown source run " + req.Source.RunID}
		}
		return &cmdError{http.StatusInternalServerError, "internal", "read source run " + req.Source.RunID + " failed"}
	}
	return nil
}

// checkTargetNamed refuses a command that names no agent (400): Studio
// never picks one for the caller (UX-C-02) — with a source run, the
// sentence says which agent recorded it, or that it names none.
func (s *Server) checkTargetNamed(ctx context.Context, req *runRequest) *cmdError {
	if req.Agent != "" {
		return nil
	}
	if req.Source != nil && req.Source.RunID != "" {
		if det, err := s.db.Run(ctx, req.Source.RunID); err == nil {
			if det.Agent == "" {
				return refuse(unnamedSourceMsg(req.Source.RunID))
			}
			return refuse(fmt.Sprintf("a run needs an agent: source run %s is agent %s (send agent: %q to replay it on that agent)", req.Source.RunID, det.Agent, det.Agent))
		}
	}
	return refuse("a run needs an agent (the connected runtimes view lists each runtime's)")
}

// unnamedSourceMsg is the refusal of a source run that recorded no
// agent name when the command names none either.
func unnamedSourceMsg(runID string) string {
	return "source run " + runID + " names no agent: name the agent to replay it on (agent), Studio does not pick one"
}

// checkTarget refuses a source run recorded by another agent than the
// one the command targets (400, UX-C-03) unless the body opts in with
// cross_agent: true — a replay is that agent's prompt and tools over
// this run's transcript, never a silent stand-in. A source that names
// no agent passes: the command named its target explicitly. Run after
// checkSource (the run exists).
func (s *Server) checkTarget(ctx context.Context, req *runRequest) *cmdError {
	if req.Source == nil || req.Source.RunID == "" || req.CrossAgent {
		return nil
	}
	det, err := s.db.Run(ctx, req.Source.RunID)
	if err != nil || det.Agent == "" || det.Agent == req.Agent {
		return nil
	}
	return refuse(crossAgentMsg(req.Source.RunID, det.Agent, req.Agent))
}

// crossAgentMsg is the cross-agent refusal's sentence: both agents and
// the escape.
func crossAgentMsg(runID, recorded, target string) string {
	return fmt.Sprintf("source run %s is agent %s; this command targets agent %s: send agent %q to replay it as recorded, or cross_agent: true to replay it as %s anyway",
		runID, recorded, target, recorded, target)
}

// checkRegistered checks the overrides against the runtime's registered
// copy of the agent — narrowing only (§6 rule 2): tools the manifest
// lacks are 400, a raised limit or a refused side-effect tool is 403.
func checkRegistered(req *runRequest, agent linkruntime.AgentRegistration) *cmdError {
	manifestTools := agent.ManifestToolNames()
	known := make(map[string]bool, len(manifestTools))
	for _, name := range manifestTools {
		known[name] = true
	}
	enabled := req.Overrides.ToolsEnabled
	for _, name := range enabled {
		if !known[name] {
			return refuse("tool " + name + " is not in agent " + agent.Name + "'s manifest")
		}
	}
	// The option lab's tool-shaped overrides (plan F3): names the
	// manifest lacks are 400; only_tools outside tools_enabled is a
	// widening, 403; a named tool_choice the run turns off or parks
	// could never be honoured, 400.
	// Version skew: a runtime older than the option lab decodes the
	// command without these fields and would run the experiment
	// without them (only_tools ignored runs every tool, park_on
	// ignored runs a ReplaySafe tool for real). A current runtime
	// always registers its defaults (tool_choice mode at least
	// "auto"); one that sends none is refused the new knobs.
	if o := req.Overrides; predatesOptionLab(agent) &&
		(o.Params != nil || o.ToolChoice != nil || len(o.ParkOn) > 0 || len(o.OnlyTools) > 0) {
		return refuse("runtime predates the option lab: upgrade weft/runtime to use params, tool_choice, park_on, only_tools")
	}
	if widens, msg := toolOverrides(req.Overrides, agent.Name, known, agent.Defaults.ToolChoice); msg != "" {
		if widens {
			return refuseWidening(msg)
		}
		return refuse(msg)
	}
	// A model outside the allow-list is the runtime's ModelResolver
	// to decide (it resolves before the ack, and its refusal is the
	// rejected command's reason); without one it is unknown here.
	if m := req.Overrides.Model; m != "" && !contains(agent.Models, m) && m != agent.ManifestModelName() && !agent.Resolver {
		return refuse("unknown model " + m + ": neither the agent's own nor a registered alternate (register it with runtime.Models or add runtime.ModelResolver)")
	}
	if e := checkNeutral(req.Overrides); e != nil {
		return e
	}
	if n := int(req.Overrides.Options["max_steps"]); n > 0 && agent.Limits.MaxSteps > 0 && n > agent.Limits.MaxSteps {
		return refuseWidening("max_steps may only lower the agent's cap")
	}
	if n := int(req.Overrides.Options["parallelism"]); n > 0 && agent.Limits.Parallelism > 0 && n > agent.Limits.Parallelism {
		return refuseWidening("parallelism may only lower the agent's cap")
	}
	if req.SideEffects == "allow" {
		touched := enabled
		if len(req.Overrides.OnlyTools) > 0 {
			touched = req.Overrides.OnlyTools // inside tools_enabled: checked above
		}
		if len(touched) == 0 {
			touched = manifestTools
		}
		// "allow" runs the AllowSideEffects tools for real; a tool
		// the code vouched ReplaySafe (or an Output agent's
		// submission) is no side effect and runs in every mode, so it
		// may stay on too — the runtime's own check is the same.
		for _, name := range touched {
			if !agent.IsAllowed(name) && agent.SideEffects[name] != "safe" && name != "submit_output" {
				return refuseWidening("tool " + name + " is not opted in for real side effects")
			}
		}
	}
	return nil
}

// predatesOptionLab reports a runtime older than the option lab (and
// than ADR 0029's edit kinds and compaction views, the same release):
// a current runtime always registers its defaults, tool_choice mode at
// least "auto"; an older one sends none.
func predatesOptionLab(agent linkruntime.AgentRegistration) bool {
	return agent.Defaults.ToolChoice.Mode == ""
}

// checkSkew refuses what a runtime older than ADR 0029 would run as
// something else (version skew; the option lab's own knobs are
// checkRegistered's). Its decoder drops an edit's kind, args and
// index, so a user or insert edit would apply its content as a reply
// rewrite and a tool_args edit not at all — acked, with no weft.edits
// mark; and it splices no compaction view, so a replay from a step
// whose request carried one would feed the model the uncompacted
// transcript. Both are 400s naming the upgrade. Nothing here reads the
// catalog: the sentences go to a read-scoped preview as they are.
func (s *Server) checkSkew(ctx context.Context, req *runRequest, agent linkruntime.AgentRegistration) *cmdError {
	if !predatesOptionLab(agent) {
		return nil
	}
	for _, e := range req.TranscriptEdits {
		if kind, err := editKindOf(e); err == nil && (kind == editUser || kind == editToolArgs || kind == editInsert) {
			return refuse("runtime predates transcript edit kinds: upgrade weft/runtime to use user, tool_args, insert edits")
		}
	}
	if req.Source != nil && req.Source.RunID != "" && req.Source.FromStep > 0 {
		sm, err := obsdb.MessagesAsOf(ctx, s.db, req.Source.RunID, req.Source.FromStep)
		if err == nil && sm.View != nil {
			return refuse(fmt.Sprintf("runtime predates replay across a compaction: step %d's request carried a compaction view the runtime would not splice in, so its model would see the uncompacted transcript; upgrade weft/runtime", req.Source.FromStep))
		}
	}
	return nil
}

// checkNeutral checks the overrides no registration is needed for: the
// thinking level, the numeric options' shape and range, the sampling
// params.
func checkNeutral(o linkruntime.Overrides) *cmdError {
	switch o.Thinking {
	case "", "off", "low", "medium", "high":
	default:
		return refuse("unknown thinking level " + o.Thinking)
	}
	for key, v := range o.Options {
		switch key {
		case "max_steps", "parallelism":
			// A whole number from 1: the lower-only checks compare it
			// as an int, and a negative, fractional or out-of-range
			// value would slip under them.
			if v < 1 || v > 1e6 || v != math.Trunc(v) {
				return refuse(key + " must be a whole number from 1")
			}
		case "temperature":
			// The range every provider accepts — the runtime's own
			// check (validOptions), answered here as a 400 instead of
			// a rejected command.
			if v < 0 || v > 2 {
				return refuse("temperature must be between 0 and 2")
			}
		default:
			return refuse("unknown option " + key)
		}
	}
	if msg := paramsOverride(o.Params); msg != "" {
		return refuse(msg)
	}
	return nil
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

// toolOverrides checks the option lab's tool-shaped overrides against
// the agent's manifest tools (known) — weft/runtime's validToolOverrides,
// answered as §10.4's statuses: msg is empty when they pass; widens
// marks the 403 (only_tools outside tools_enabled), every other refusal
// is a 400. Without a tool_choice override the agent's registered
// default (def) must still name a tool the command leaves on.
func toolOverrides(o linkruntime.Overrides, agent string, known map[string]bool, def linkruntime.ToolChoice) (widens bool, msg string) {
	for _, name := range o.OnlyTools {
		if !known[name] {
			return false, "tool " + name + " in only_tools is not in agent " + agent + "'s manifest"
		}
		if len(o.ToolsEnabled) > 0 && !contains(o.ToolsEnabled, name) {
			return true, "only_tools may only narrow tools_enabled: tool " + name + " is not enabled"
		}
	}
	for _, name := range o.ParkOn {
		if !known[name] {
			return false, "tool " + name + " in park_on is not in agent " + agent + "'s manifest"
		}
	}
	tc := o.ToolChoice
	if tc == nil {
		if def.Mode == "named" && !leavesOn(o, def.Name) {
			return false, "the agent's default tool_choice names " + def.Name + ", which this command turns off; send tool_choice"
		}
		if def.Mode == "named" && contains(o.ParkOn, def.Name) {
			// The explicit case's rule and sentence (below): a default
			// that forces a tool park_on parks makes every forced call
			// park, whoever named it.
			return false, parkedChoice(def.Name)
		}
		return false, ""
	}
	switch tc.Mode {
	case "", "auto", "any", "none":
		if tc.Name != "" {
			return false, "tool_choice mode " + orAuto(tc.Mode) + " takes no name (only named does)"
		}
		return false, ""
	case "named":
	default:
		return false, "unknown tool_choice mode " + tc.Mode + " (auto, any, none or named)"
	}
	on := leavesOn(o, tc.Name)
	switch {
	case tc.Name == "":
		return false, "tool_choice named needs a tool name"
	case !known[tc.Name]:
		return false, "tool " + tc.Name + " in tool_choice is not in agent " + agent + "'s manifest"
	case !on:
		return false, "tool_choice names " + tc.Name + ", which this command turns off"
	case contains(o.ParkOn, tc.Name):
		return false, parkedChoice(tc.Name)
	}
	return false, ""
}

// parkedChoice is the refusal of a named tool_choice — the command's or
// the agent's default — that park_on parks, in weft/runtime's words
// (executor.go's parkedChoice; TestOneSentenceOnBothSides).
func parkedChoice(name string) string {
	return "tool_choice names " + name + ", which park_on parks: every forced call would park"
}

// leavesOn reports whether the command leaves tool name on: only_tools
// when set, else tools_enabled, else every tool.
func leavesOn(o linkruntime.Overrides, name string) bool {
	switch {
	case len(o.OnlyTools) > 0:
		return contains(o.OnlyTools, name)
	case len(o.ToolsEnabled) > 0:
		return contains(o.ToolsEnabled, name)
	}
	return true
}

// orAuto spells an empty tool_choice mode as the auto it means.
func orAuto(mode string) string {
	if mode == "" {
		return "auto"
	}
	return mode
}

// paramsOverride checks the sampling override (neutral knobs) as
// weft/runtime's validParams does: top_p inside 0..1, a positive
// max_tokens, at most four non-empty stop sequences; any seed. Empty
// when it passes.
func paramsOverride(p *linkruntime.Params) string {
	if p == nil {
		return ""
	}
	if v := p.TopP; v != nil && (*v < 0 || *v > 1) {
		return "top_p must be between 0 and 1"
	}
	if v := p.MaxTokens; v != nil && *v <= 0 {
		return "max_tokens must be positive"
	}
	if len(p.Stop) > 4 {
		return fmt.Sprintf("stop takes at most 4 sequences, got %d", len(p.Stop))
	}
	for _, s := range p.Stop {
		if s == "" {
			return "stop sequences must be non-empty"
		}
	}
	return ""
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
// An app-owned turn is decided through a runtime that registered
// runtime.Sessions (plan F4, appturn.go: 202 with the command to poll)
// and refused 403 "app-owned turn" otherwise.
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
		if !started && dbErr != nil {
			// Not a runtime-started run, and not stored: a 404.
			if errors.Is(dbErr, obsdb.ErrNotFound) {
				notFound(w, r, "unknown run "+runID)
				return
			}
			dbError(w, r, "run", runID, dbErr)
			return
		}
		// A panel token decides inside its public id only (S4.6): the
		// scope the command carried, or the row's when it has landed.
		// The decision's own command carries the same scope — the page
		// polls it, and its acks name the resumed run.
		publicID := ""
		if started {
			publicID, _ = rs.PublicOf(runID)
		}
		if publicID == "" && dbErr == nil {
			publicID = row.PublicID
		}
		if pid := idFrom(r).panel; pid != nil && publicID != pid.PublicID {
			forbidden(w, r)
			return
		}
		appTarget := ""
		if !started {
			// The app's own turn (plan F4): decided through a runtime
			// installed with runtime.Sessions — the app's Session records
			// the decision and resumes the turn — else 403 "app-owned
			// turn" before the call is looked at.
			target, ok := appTurnRuntime(w, r, rs, row.RunRow)
			if !ok {
				return
			}
			appTarget = target
		}
		// The call must be one the parked run is waiting on (the
		// audit's P2-16): the core resumes with exactly the decisions it
		// is handed and answers every other pending call "DENIED: no
		// decision", so a stale or mistyped id would silently deny the
		// call the human meant to decide. The run's own run_finish names
		// the pending set; until it has landed there is nothing to check
		// against and the runtime's own check is the only one (for an
		// app turn, the session's own Pending).
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
		if appTarget != "" {
			sc := appSessionCommand(r, linkruntime.VerbDecide, row.RunRow)
			sc.CallID, sc.Decision, sc.Reason, sc.Content = req.CallID, req.Decision, req.Reason, req.Content
			enqueueAppTurn(w, r, rs, appTarget, sc)
			return
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
