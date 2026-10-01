// Package runtime is the runtime link's server side (WEFT-PLAYGROUND
// §10.3, S4.2): the registry of connected runtimes and the three
// routes a weft/runtime client speaks to — register, the SSE command
// stream, acks. Studio decides (the playground routes in
// studio/playground.go enqueue commands against a registered agent);
// the runtime in your app executes.
//
// The package is self-contained on purpose: it holds no database and
// reads nothing — a command's validation data is the runtime's own
// registration, refreshed on every reconnect, because the runtime's
// copy is the authoritative one (§10.4). Commands are at-most-once:
// the runtime acks before executing and ignores repeated ids; Studio
// refuses a reused command id outright (409) and never re-sends a
// lost one — the user re-issues it (§10.5).
//
// Command lifecycle (§10.5), with its two timers:
//
//	queued ──(ack)──► accepted ──(finish)──► finished
//	   │                 │
//	   ├──(reject)──► rejected
//	   └──(no ack in 30 s, or the stream died before the ack)──► lost
//	accepted ──(stream died, no finish in 10 min)──► lost
//
// A late ack or finish still lands: the truth about a run wins over a
// timer's guess.
package runtime

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// The wire vocabulary (§10.3). Mirrored — deliberately, with the JSON
// pinned by tests — in the weft/runtime module, which must not import
// this package (its studio imports stay at *studio.Server alone).

// Registration is the POST /api/runtime/register body.
type Registration struct {
	RuntimeID   string              `json:"runtime_id"`
	Host        string              `json:"host"`
	Pid         int                 `json:"pid"`
	Service     string              `json:"service"`
	Env         string              `json:"env"`
	WeftVersion string              `json:"weft_version"`
	Budget      Budget              `json:"budget"`
	Threads     bool                `json:"threads"`
	Agents      []AgentRegistration `json:"agents"`
}

// AgentRegistration is one exposed agent.
type AgentRegistration struct {
	Name        string            `json:"name"`
	Manifest    string            `json:"manifest"`
	Models      []string          `json:"models"`
	Limits      AgentLimits       `json:"limits"`
	SideEffects map[string]string `json:"side_effects"`
	Allow       []string          `json:"allow"`
}

// AgentLimits are the agent's own lower-only bounds.
type AgentLimits struct {
	MaxSteps    int `json:"max_steps"`
	Parallelism int `json:"parallelism"`
}

// Budget is the runtime's per-experiment caps.
type Budget struct {
	MaxTokensPerExperiment int64 `json:"max_tokens_per_experiment"`
	MaxRunsPerExperiment   int64 `json:"max_runs_per_experiment"`
}

// Agent is an agent lookup on a registration.
func (r Registration) Agent(name string) (AgentRegistration, bool) {
	for _, a := range r.Agents {
		if a.Name == name {
			return a, true
		}
	}
	return AgentRegistration{}, false
}

// IsAllowed reports whether tool is on the agent's AllowSideEffects
// list.
func (a AgentRegistration) IsAllowed(tool string) bool {
	for _, t := range a.Allow {
		if t == tool {
			return true
		}
	}
	return false
}

// ManifestToolNames lists the agent's tools in manifest order.
func (a AgentRegistration) ManifestToolNames() []string {
	var doc struct {
		Agents []struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(a.Manifest), &doc); err != nil || len(doc.Agents) == 0 {
		return nil
	}
	names := make([]string, 0, len(doc.Agents[0].Tools))
	for _, t := range doc.Agents[0].Tools {
		names = append(names, t.Name)
	}
	return names
}

// ManifestModelName returns the agent's own model name.
func (a AgentRegistration) ManifestModelName() string {
	var doc struct {
		Agents []struct {
			Model struct {
				Name string `json:"name"`
			} `json:"model"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(a.Manifest), &doc); err != nil || len(doc.Agents) == 0 {
		return ""
	}
	return doc.Agents[0].Model.Name
}

// RegisterResponse answers a registration.
type RegisterResponse struct {
	RuntimeID   string `json:"runtime_id"`
	CommandsURL string `json:"commands_url"`
}

// Command is one `event: run` frame's data (§10.3). Runtime, seq and
// cancel are the server's own bookkeeping and stay off the wire.
type Command struct {
	CommandID       string           `json:"command_id"`
	Agent           string           `json:"agent"`
	Source          *SourceSpec      `json:"source"`
	Input           *string          `json:"input"`
	Overrides       Overrides        `json:"overrides"`
	TranscriptEdits []TranscriptEdit `json:"transcript_edits"`
	Engine          string           `json:"engine"`
	SideEffects     string           `json:"side_effects"`
	Thread          string           `json:"thread"`
	ExperimentID    string           `json:"experiment_id"`
	Actor           string           `json:"actor"`
	PublicID        string           `json:"public_id"`

	// Runtime is the runtime the command was enqueued to.
	Runtime string `json:"-"`
	// cancel marks a cancel frame instead of a run (Cancel).
	cancel bool `json:"-"`
	// seq orders commands within this server (a monotonic counter,
	// independent of the id's own ordering).
	seq uint64
}

// SourceSpec names the run to re-run.
type SourceSpec struct {
	RunID    string `json:"run_id"`
	FromStep int    `json:"from_step"`
}

// Overrides are the experiment's changes (§5.1).
type Overrides struct {
	Instructions string             `json:"instructions,omitempty"`
	ToolsEnabled []string           `json:"tools_enabled,omitempty"`
	Model        string             `json:"model,omitempty"`
	Thinking     string             `json:"thinking,omitempty"`
	Options      map[string]float64 `json:"options,omitempty"`
}

// TranscriptEdit is a D2/D3 edit; accepted by the schema, answered
// "not yet available" until 8b.
type TranscriptEdit struct {
	Step       int    `json:"step"`
	ToolResult string `json:"tool_result,omitempty"`
	CallID     string `json:"call_id,omitempty"`
	Content    string `json:"content,omitempty"`
}

// Ack is the POST /api/runtime/acks body: accepted/rejected before
// execution, finished at the run's end.
type Ack struct {
	CommandID string `json:"command_id"`
	State     string `json:"state"`
	RunID     string `json:"run_id,omitempty"`
	Status    string `json:"status,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Command states (§10.5).
const (
	StateQueued   = "queued"
	StateAccepted = "accepted"
	StateRejected = "rejected"
	StateFinished = "finished"
	StateLost     = "lost"
)

// CommandStatus is the GET /api/playground/commands/{id} row (§10.4).
type CommandStatus struct {
	CommandID string    `json:"command_id"`
	State     string    `json:"state"`
	RunID     string    `json:"run_id"`
	Error     *string   `json:"error"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
}

// RuntimeView is one connected runtime in GET /api/runtimes (§10.4's
// shape, pinned): identity, liveness, and per agent the alternate
// models and every tool with its side-effect class and allow flag.
type RuntimeView struct {
	ID             string      `json:"id"`
	Host           string      `json:"host"`
	Pid            int         `json:"pid"`
	Service        string      `json:"service"`
	Env            string      `json:"env"`
	ConnectedSince time.Time   `json:"connected_since"`
	LastSeen       time.Time   `json:"last_seen"`
	Agents         []AgentView `json:"agents"`
}

// AgentView is one agent of a connected runtime.
type AgentView struct {
	Name   string     `json:"name"`
	Models []string   `json:"models"`
	Tools  []ToolView `json:"tools"`
}

// ToolView is one tool of a connected runtime's agent.
type ToolView struct {
	Name        string `json:"name"`
	SideEffects string `json:"side_effects"`
	Allow       bool   `json:"allow"`
}

// Errors Enqueue and the routes answer with.
var (
	// ErrUnknownRuntime: no runtime ever registered under the id.
	ErrUnknownRuntime = fmt.Errorf("studio/runtime: unknown runtime")
	// ErrNotConnected: the runtime registered once but holds no live
	// command stream now.
	ErrNotConnected = fmt.Errorf("studio/runtime: runtime not connected")
	// ErrDuplicateCommand: the command id was already used (409, the
	// at-most-once rule's Studio side).
	ErrDuplicateCommand = fmt.Errorf("studio/runtime: command id already used")
	// ErrUnknownCommand: no command under the id.
	ErrUnknownCommand = fmt.Errorf("studio/runtime: unknown command")
)

// RuntimeServer is the registry of connected runtimes and their
// commands: it mounts the three §10.3 routes (Mount), hands the
// playground its views and its enqueue (Snapshot, Registration,
// Enqueue, Command), and runs §10.5's lost-command timers. It is the
// type Server.Runtime returns once the studio side wires it (S4.1;
// see notes-lane-c2.md for the studio.go/routes.go lines that merge
// applies — this lane owns neither file).
type RuntimeServer struct {
	// AckDeadline is how long a queued command may go unacked before
	// it is lost (§10.5: 30 s). A field so tests can tighten it.
	AckDeadline time.Duration
	// FinishDeadline is how long an accepted command may stay
	// unfinished after its runtime's stream died before it is lost
	// (§10.5: 10 min). A field so tests can tighten it.
	FinishDeadline time.Duration
	// PingEvery is the SSE keep-alive cadence (15 s).
	PingEvery time.Duration
	// feedSize bounds one stream's queued frames (obsdb.QueueSize's
	// spirit); beyond it the stream is dropped and the runtime
	// reconnects.
	feedSize int

	mu       sync.Mutex
	runtimes map[string]*connected
	commands map[string]*commandRow
	nextSeq  uint64
	now      func() time.Time
}

// connected is one registered runtime and its live command stream.
type connected struct {
	reg            Registration
	connectedSince time.Time
	lastSeen       time.Time
	feed           chan Command // nil when no live stream
}

// commandRow is one command's lifecycle state.
type commandRow struct {
	Command
	state     string
	runID     string
	status    string
	errText   string
	created   time.Time
	updated   time.Time
	ackTimer  *time.Timer
	lostTimer *time.Timer
}

// New builds an empty RuntimeServer with §10.5's timers.
func New() *RuntimeServer {
	return &RuntimeServer{
		AckDeadline:    30 * time.Second,
		FinishDeadline: 10 * time.Minute,
		PingEvery:      15 * time.Second,
		feedSize:       256,
		runtimes:       map[string]*connected{},
		commands:       map[string]*commandRow{},
		now:            time.Now,
	}
}

// Mount registers the three runtime-link routes (§10.3) on mux. The
// studio server mounts them under its /api tree, so its token gate
// (the Studio destination's token, S4.6) applies: nothing in-process
// (setup A), the bearer token otherwise.
func (rs *RuntimeServer) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/runtime/register", rs.serveRegister)
	mux.HandleFunc("GET /api/runtime/commands", rs.serveCommands)
	mux.HandleFunc("POST /api/runtime/acks", rs.serveAcks)
}

// ── register ──────────────────────────────────────────────────────

// serveRegister is POST /api/runtime/register: a runtime's current
// self-description. Sent on connect and after every reconnect, so
// this handler may overwrite a previous registration freely.
func (rs *RuntimeServer) serveRegister(w http.ResponseWriter, r *http.Request) {
	var reg Registration
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		writeErr(w, r, http.StatusBadRequest, "bad_request", "register body: "+err.Error())
		return
	}
	if reg.RuntimeID == "" {
		writeErr(w, r, http.StatusBadRequest, "bad_request", "register body: runtime_id is required")
		return
	}
	if len(reg.Agents) == 0 {
		writeErr(w, r, http.StatusBadRequest, "bad_request", "register body: no agents")
		return
	}
	rs.mu.Lock()
	c := rs.runtimes[reg.RuntimeID]
	if c == nil {
		c = &connected{}
		rs.runtimes[reg.RuntimeID] = c
	}
	c.reg = reg
	c.lastSeen = rs.now()
	if c.connectedSince.IsZero() {
		c.connectedSince = c.lastSeen
	}
	rs.mu.Unlock()
	slog.Debug("studio/runtime: registered", "runtime_id", reg.RuntimeID, "agents", len(reg.Agents))
	writeJSON(w, r, http.StatusOK, RegisterResponse{
		RuntimeID:   reg.RuntimeID,
		CommandsURL: "/api/runtime/commands?runtime=" + reg.RuntimeID,
	})
}

// touch notes the runtime was just seen.
func (rs *RuntimeServer) touch(id string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if c := rs.runtimes[id]; c != nil {
		c.lastSeen = rs.now()
	}
}

// ── the command stream (SSE) ──────────────────────────────────────

// serveCommands is GET /api/runtime/commands?runtime=<id> — the
// long-lived SSE stream (§10.3). Frame ids are command ids; a
// reconnect's Last-Event-ID names the newest command the runtime
// saw, and any still-queued commands after it are re-sent (the
// runtime's own seen-set makes that harmless; §5.3 at-most-once).
func (rs *RuntimeServer) serveCommands(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("runtime")
	if id == "" {
		writeErr(w, r, http.StatusBadRequest, "bad_request", "the commands stream needs ?runtime=<id>")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, r, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}
	rs.mu.Lock()
	c := rs.runtimes[id]
	if c == nil {
		rs.mu.Unlock()
		writeErr(w, r, http.StatusNotFound, "not_found", "unknown runtime "+id)
		return
	}
	// Replace any previous stream: a runtime holds one.
	feed := make(chan Command, rs.feedSize)
	c.feed = feed
	c.lastSeen = rs.now()
	if c.connectedSince.IsZero() {
		c.connectedSince = c.lastSeen
	}
	backlog := rs.backlogLocked(id, r.Header.Get("Last-Event-ID"))
	rs.mu.Unlock()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "event: ping\ndata: {}\n\n")
	flusher.Flush()
	for _, cmd := range backlog {
		writeRunFrame(w, flusher, cmd)
	}

	ping := time.NewTicker(rs.PingEvery)
	defer ping.Stop()
	ctx := r.Context()
	for {
		select {
		case cmd := <-feed:
			writeRunFrame(w, flusher, cmd)
		case <-ping.C:
			rs.touch(id)
			_, _ = fmt.Fprint(w, "event: ping\ndata: {}\n\n")
			flusher.Flush()
		case <-ctx.Done():
			rs.streamEnded(id, feed)
			return
		}
	}
}

// backlogLocked re-delivers the queued commands after the resume
// cursor (a command id; "" means from the start). Caller holds mu.
func (rs *RuntimeServer) backlogLocked(runtimeID, lastEventID string) []Command {
	c := rs.runtimes[runtimeID]
	if c == nil {
		return nil
	}
	afterSeq := uint64(0)
	if lastEventID != "" {
		if row := rs.commands[lastEventID]; row != nil {
			afterSeq = row.seq
		} else if strings.HasPrefix(lastEventID, "cmd_") {
			// An id we never minted: treat as "nothing after" rather
			// than re-sending everything (a fresh registration that
			// lost the server's memory must not re-run commands).
			return nil
		}
	}
	var out []Command
	for _, row := range rs.commands {
		if row.Runtime != "" && row.Runtime != runtimeID {
			continue
		}
		if row.state == StateQueued && row.seq > afterSeq {
			out = append(out, row.Command)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

// writeRunFrame writes one command frame — `event: run` with the
// command id as its SSE id, or `event: cancel` (§10.3's shapes).
func writeRunFrame(w http.ResponseWriter, flusher http.Flusher, cmd Command) {
	if cmd.cancel {
		_, _ = fmt.Fprintf(w, "event: cancel\ndata: {\"command_id\":%q}\n\n", cmd.CommandID)
		flusher.Flush()
		return
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "id: %s\nevent: run\ndata: %s\n\n", cmd.CommandID, data)
	flusher.Flush()
}

// streamEnded is the disconnect sweep (§10.5): queued commands of this
// runtime are lost at once; accepted ones get FinishDeadline to land
// a finish before they are lost too.
func (rs *RuntimeServer) streamEnded(id string, feed chan Command) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	c := rs.runtimes[id]
	if c == nil || c.feed != feed {
		return // a newer stream replaced this one
	}
	c.feed = nil
	c.lastSeen = rs.now()
	for _, row := range rs.commands {
		if row.Runtime != id {
			continue
		}
		switch row.state {
		case StateQueued:
			rs.transitionLocked(row, StateLost, "runtime disconnected before the ack")
		case StateAccepted:
			rs.armLostLocked(row, rs.FinishDeadline, "runtime disconnected, no finish")
		}
	}
}

// ── acks ──────────────────────────────────────────────────────────

// serveAcks is POST /api/runtime/acks: the runtime's word on a
// command — accepted/rejected before execution (at-most-once),
// finished with the run's status at the end. A late ack still lands:
// the truth wins over the timers.
func (rs *RuntimeServer) serveAcks(w http.ResponseWriter, r *http.Request) {
	var a Ack
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeErr(w, r, http.StatusBadRequest, "bad_request", "ack body: "+err.Error())
		return
	}
	if a.CommandID == "" {
		writeErr(w, r, http.StatusBadRequest, "bad_request", "ack body: command_id is required")
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	row := rs.commands[a.CommandID]
	if row == nil {
		writeErr(w, r, http.StatusNotFound, "not_found", "unknown command "+a.CommandID)
		return
	}
	rs.stopTimersLocked(row)
	switch a.State {
	case "accepted":
		if row.state == StateQueued || row.state == StateLost {
			row.state = StateAccepted
			row.runID = a.RunID
			row.updated = rs.now()
		}
	case "rejected":
		if row.state == StateQueued || row.state == StateLost {
			row.state = StateRejected
			row.errText = a.Error
			row.updated = rs.now()
		}
	case "finished":
		row.state = StateFinished
		row.runID = orDefault(a.RunID, row.runID)
		row.status = a.Status
		row.updated = rs.now()
	default:
		writeErr(w, r, http.StatusBadRequest, "bad_request", "ack body: unknown state "+a.State)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ── the playground's side ─────────────────────────────────────────

// Enqueue validates nothing (that is the playground route's job) and
// hands cmd to the named runtime's stream, minting the command id
// when the caller did not. Errors: ErrUnknownRuntime (404),
// ErrNotConnected (503), ErrDuplicateCommand (409).
func (rs *RuntimeServer) Enqueue(runtimeID string, cmd Command) (Command, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	c := rs.runtimes[runtimeID]
	if c == nil {
		return cmd, ErrUnknownRuntime
	}
	if cmd.CommandID == "" {
		cmd.CommandID = newCommandID()
	}
	if _, dup := rs.commands[cmd.CommandID]; dup {
		return cmd, ErrDuplicateCommand
	}
	if c.feed == nil {
		// Not connected: nothing to deliver to (the route checks
		// before enqueuing, so this is a lost race with a
		// disconnect).
		return cmd, ErrNotConnected
	}
	cmd.Runtime = runtimeID
	cmd.seq = rs.nextSeq + 1
	rs.nextSeq = cmd.seq
	now := rs.now()
	row := &commandRow{Command: cmd, state: StateQueued, created: now, updated: now}
	rs.commands[cmd.CommandID] = row
	select {
	case c.feed <- cmd:
	default:
		// A full feed is a stalled stream: drop it (the runtime
		// reconnects; the backlog re-sends what is still queued).
		c.feed = nil
	}
	rs.armAckLocked(row)
	return cmd, nil
}

// Cancel sends an `event: cancel` frame for a command (the debugger
// rungs' verb; not a P0 route). Best effort: the runtime cancels what
// it started, and never the app's own runs.
func (rs *RuntimeServer) Cancel(runtimeID, commandID string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if c := rs.runtimes[runtimeID]; c != nil && c.feed != nil {
		select {
		case c.feed <- Command{CommandID: commandID, Runtime: runtimeID, cancel: true}:
		default:
		}
	}
}

// Command returns the command's lifecycle row; ErrUnknownCommand
// otherwise.
func (rs *RuntimeServer) Command(id string) (CommandStatus, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	row := rs.commands[id]
	if row == nil {
		return CommandStatus{}, ErrUnknownCommand
	}
	return row.view(), nil
}

// CommandOf returns the command as enqueued (its public id names the
// page a panel token is scoped to); ok is false for an unknown id.
func (rs *RuntimeServer) CommandOf(id string) (Command, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	row := rs.commands[id]
	if row == nil {
		return Command{}, false
	}
	return row.Command, true
}

// Registration returns the runtime's current registration (the
// authoritative copy of its agents, allow-lists and caps) when it has
// registered; ok is false for an unknown id.
func (rs *RuntimeServer) Registration(runtimeID string) (Registration, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	c := rs.runtimes[runtimeID]
	if c == nil {
		return Registration{}, false
	}
	return c.reg, true
}

// Connected reports whether the runtime holds a live command stream.
func (rs *RuntimeServer) Connected(runtimeID string) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	c := rs.runtimes[runtimeID]
	return c != nil && c.feed != nil
}

// Snapshot lists the runtimes that ever registered, newest-seen
// first, with their liveness and agents in §10.4's shape. A runtime
// that registered but holds no stream stays visible until its
// FinishDeadline-watched commands resolve (the UI reads last_seen).
func (rs *RuntimeServer) Snapshot() []RuntimeView {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	views := make([]RuntimeView, 0, len(rs.runtimes))
	for id, c := range rs.runtimes {
		v := RuntimeView{
			ID:             id,
			Host:           c.reg.Host,
			Pid:            c.reg.Pid,
			Service:        c.reg.Service,
			Env:            c.reg.Env,
			ConnectedSince: c.connectedSince,
			LastSeen:       c.lastSeen,
		}
		for _, a := range c.reg.Agents {
			av := AgentView{Name: a.Name, Models: a.Models}
			if av.Models == nil {
				av.Models = []string{}
			}
			for _, name := range a.ManifestToolNames() {
				av.Tools = append(av.Tools, ToolView{
					Name:        name,
					SideEffects: a.SideEffects[name],
					Allow:       a.IsAllowed(name),
				})
			}
			if av.Tools == nil {
				av.Tools = []ToolView{}
			}
			v.Agents = append(v.Agents, av)
		}
		if v.Agents == nil {
			v.Agents = []AgentView{}
		}
		views = append(views, v)
	}
	sort.Slice(views, func(i, j int) bool {
		if !views[i].LastSeen.Equal(views[j].LastSeen) {
			return views[i].LastSeen.After(views[j].LastSeen)
		}
		return views[i].ID < views[j].ID
	})
	return views
}

// ── the lifecycle timers (§10.5) ──────────────────────────────────

// armAckLocked starts the 30 s unacked timer on a queued command.
func (rs *RuntimeServer) armAckLocked(row *commandRow) {
	if rs.AckDeadline <= 0 {
		return
	}
	row.ackTimer = time.AfterFunc(rs.AckDeadline, func() {
		rs.mu.Lock()
		defer rs.mu.Unlock()
		if row.state == StateQueued {
			rs.transitionLocked(row, StateLost, "no ack in time")
		}
	})
}

// armLostLocked starts a finish watch on an accepted command.
func (rs *RuntimeServer) armLostLocked(row *commandRow, d time.Duration, why string) {
	if d <= 0 {
		return
	}
	row.lostTimer = time.AfterFunc(d, func() {
		rs.mu.Lock()
		defer rs.mu.Unlock()
		if row.state == StateAccepted {
			rs.transitionLocked(row, StateLost, why)
		}
	})
}

// transitionLocked moves a row to a terminal state with a reason.
func (rs *RuntimeServer) transitionLocked(row *commandRow, state, why string) {
	rs.stopTimersLocked(row)
	row.state = state
	row.errText = why
	row.updated = rs.now()
}

// stopTimersLocked cancels both timers.
func (rs *RuntimeServer) stopTimersLocked(row *commandRow) {
	if row.ackTimer != nil {
		row.ackTimer.Stop()
		row.ackTimer = nil
	}
	if row.lostTimer != nil {
		row.lostTimer.Stop()
		row.lostTimer = nil
	}
}

// view renders the row in §10.4's shape.
func (row *commandRow) view() CommandStatus {
	var err *string
	if row.errText != "" {
		text := row.errText
		err = &text
	}
	return CommandStatus{
		CommandID: row.CommandID,
		State:     row.state,
		RunID:     row.runID,
		Error:     err,
		Created:   row.created,
		Updated:   row.updated,
	}
}

func orDefault(s, def string) string {
	if s != "" {
		return s
	}
	return def
}
