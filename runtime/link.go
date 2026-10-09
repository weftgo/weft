package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/thread"
)

// The link's bounds. Everything that arrives over the stream is
// untrusted — Studio may be stale, buggy, or not Studio — so nothing it
// sends grows this process without limit.
const (
	// maxSeen is how many command ids the at-most-once set remembers.
	// Studio re-sends only commands still queued (unacked, so younger
	// than its 30 s ack deadline, §10.5), and refuses a reused id
	// itself (409); 4096 newer commands inside that window is far past
	// what a link carries.
	maxSeen = 4096
	// maxCommands bounds the commands admitted and not yet finished —
	// running or waiting for a run slot. One past it is rejected.
	maxCommands = 256
	// maxRunning bounds the runs executing at once: a 3×20 matrix
	// lands as sixty commands in one burst, and sixty model streams at
	// once is the provider 429 the budget caps exist to avoid (§6
	// rule 6). The rest wait, already acked.
	maxRunning = 16
	// maxFrameBytes bounds one SSE frame (a command with its
	// instructions and input). A longer one ends the stream.
	maxFrameBytes = 8 << 20
	// The HTTP bounds: a register must answer, response headers must
	// arrive (the stream's body then stays open as long as it likes),
	// and a stream counts as healthy — the backoff starts over — once
	// it has lived this long.
	registerTimeout = 15 * time.Second
	headerTimeout   = 30 * time.Second
	healthyStream   = 10 * time.Second
	// maxAckError bounds the failure text an ack carries (the full
	// error is in this process's log).
	maxAckError = 2 << 10
	// stopWait bounds how long stop waits for the link goroutine, and
	// then for the canceled runs, to end.
	stopWait = 5 * time.Second
)

// link is the runtime link's client side: it dials out to Studio,
// registers on connect and after every reconnect, receives commands
// on a long-lived SSE stream (resuming with Last-Event-ID), acks every
// command before executing it, and reports the run's end. Commands
// are at-most-once: a repeated command id is ignored (§5.3).
type link struct {
	cfg    *config
	reg    *registry
	client *http.Client
	base   string // Studio's, no trailing slash; "" in-process (URLs are absolute anyway)
	token  string
	// localDB is the source path 2's database: otel.LocalDB, read per
	// command (a field so a test aims it at its own sink).
	localDB func() obsdb.DB

	id string // this runtime's id, stable for the link's lifetime

	ctx    context.Context // the link's life: every run this link starts hangs off it
	cancel context.CancelFunc
	done   chan struct{}
	// started is set by start, before its goroutine exists: stop waits
	// for the connect loop only when there is one.
	started bool
	runs    sync.WaitGroup // the dispatch goroutines
	slots   chan struct{}  // maxRunning run slots

	mu          sync.Mutex
	stopped     bool
	seen        map[string]bool // command ids already admitted (at-most-once)
	seenOrder   []string        // the same ids, oldest first — the eviction order
	lastID      string          // the newest command frame received: the resume cursor
	inFlight    map[string]context.CancelFunc
	tally       map[string]*budgetState      // per experiment_id
	parked      map[string]*parkedRun        // run id → the parked run this runtime started
	parkOrder   []string                     // parked run ids, oldest first
	forks       map[string]*thread.Session   // sessions this runtime forked (their turns continue in place)
	forkCmd     map[string]command           // fork session id → the command its latest turn ran (a rebuilt park's shaping)
	forkOrder   []string                     // forked session ids, oldest first
	breakpoints map[string]bool              // the debugger's tool set (§8.3): parked on every run
	steerQ      map[string]chan core.Message // run id → the in-flight run's steering queue
	steerSess   map[string]forkSteer         // fork turns in flight: steered through the session

	reconnect    func() time.Duration // backoff; indirected by tests
	resetBackoff func()
	refused      sync.Once // the one WARN for a register Studio's token gate refused
}

// newLink wires the client. In-process when cfg.local is set (no
// socket), else a plain HTTP client on url.
func newLink(c *config, reg *registry, url, token string) *link {
	l := &link{
		cfg:         c,
		reg:         reg,
		localDB:     otel.LocalDB,
		token:       token,
		id:          newID("rt_"),
		done:        make(chan struct{}),
		slots:       make(chan struct{}, maxRunning),
		seen:        map[string]bool{},
		inFlight:    map[string]context.CancelFunc{},
		tally:       map[string]*budgetState{},
		parked:      map[string]*parkedRun{},
		forks:       map[string]*thread.Session{},
		forkCmd:     map[string]command{},
		breakpoints: map[string]bool{},
		steerQ:      map[string]chan core.Message{},
		steerSess:   map[string]forkSteer{},
	}
	l.ctx, l.cancel = context.WithCancel(context.Background())
	if c.local != nil {
		l.client = inProcessClient(c.local)
	} else {
		l.base = strings.TrimRight(url, "/")
		// No client timeout — the SSE stream is long-lived — but the
		// response headers must arrive: a Studio that accepts the
		// connection and then says nothing does not hold the link.
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.ResponseHeaderTimeout = headerTimeout
		l.client = &http.Client{Transport: tr}
	}
	bo := newBackoff()
	l.reconnect, l.resetBackoff = bo.next, bo.reset
	return l
}

// start runs the connect loop on its own goroutine. It returns an
// error only if the link cannot even construct its request (never in
// practice); every network failure is retried with backoff — a dev
// tool must not fail the program (§6 rule 1's spirit).
func (l *link) start() error {
	ctx := l.ctx
	l.started = true
	go func() {
		defer close(l.done)
		for {
			if l.connect(ctx) == nil {
				return // context canceled: shutdown
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(l.reconnect()):
			}
		}
	}()
	return nil
}

// stop shuts the link down: the stream ends, every run this link
// started is canceled (they hang off the link's context — a stopped
// runtime runs no tools and spends no tokens), and stop waits, bounded,
// for both. Safe to call more than once.
func (l *link) stop() {
	l.mu.Lock()
	l.stopped = true // no command is admitted from here on
	l.mu.Unlock()
	l.cancel()
	if l.started {
		select {
		case <-l.done:
		case <-time.After(stopWait):
		}
	}
	idle := make(chan struct{})
	go func() {
		l.runs.Wait()
		close(idle)
	}()
	select {
	case <-idle:
	case <-time.After(stopWait):
	}
	// The forks' Sessions hold their writer leases until closed: give
	// them up, so the app (or the next process) can write those
	// sessions. Bounded like the waits above.
	released := make(chan struct{})
	go func() {
		l.releaseHeld(context.Background())
		close(released)
	}()
	select {
	case <-released:
	case <-time.After(stopWait):
	}
	if tr, ok := l.client.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}

// connect does one full session: register, then stream commands until
// the stream or the context ends. A nil return means shutdown; any
// error means reconnect.
func (l *link) connect(ctx context.Context) error {
	if err := l.register(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		slog.Debug("weft/runtime: register failed; retrying", "err", err)
		return err
	}
	err := l.stream(ctx)
	if ctx.Err() != nil {
		return nil
	}
	if err == nil {
		err = io.EOF
	}
	slog.Debug("weft/runtime: command stream ended; reconnecting", "err", err)
	return err
}

// register is POST /api/runtime/register — on connect and again after
// every reconnect, so Studio's copy of the agents, allow-lists and
// caps is always the runtime's own (§10.3).
func (l *link) register(ctx context.Context) error {
	reg := l.reg.registration(l.id)
	reg.Breakpoints = l.breakpointSet()
	body, err := json.Marshal(reg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, registerTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.url("/api/runtime/register"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	bearerAuth(req, l.token)
	resp, err := l.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			// Retrying cannot fix a refused token, and the retry loop
			// only logs at Debug: say it once where someone will see it.
			l.refused.Do(func() {
				slog.Warn("weft/runtime: Studio refused the runtime link; the playground stays off until the token is right",
					"status", resp.Status,
					"hint", "runtime.Studio(url, token) carries the token — with runtime.Local(srv) too, when srv was built with studio.Token")
			})
		}
		return fmt.Errorf("weft/runtime: register: %s", resp.Status)
	}
	var out registerResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return err
	}
	if out.RuntimeID != "" {
		l.id = out.RuntimeID
	}
	return nil
}

// stream is the command stream: GET commands_url, SSE, long-lived
// (§10.3). Returns when the stream breaks (reconnect follows) or the
// link shuts down (nil error is not required; the caller checks the
// context).
func (l *link) stream(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		l.url("/api/runtime/commands?runtime="+url.QueryEscape(l.id)), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	if last := l.lastEventID(); last != "" {
		req.Header.Set("Last-Event-ID", last)
	}
	bearerAuth(req, l.token)
	resp, err := l.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("weft/runtime: commands: %s", resp.Status)
	}
	opened := time.Now()
	err = l.readStream(ctx, resp.Body)
	if time.Since(opened) >= healthyStream {
		// The link was up: the next break starts the backoff over
		// instead of inheriting every failure since the process began.
		l.resetBackoff()
	}
	return err
}

// readStream parses the SSE frames and dispatches them. The frame ids
// are command ids; the newest one seen is the resume cursor.
func (l *link) readStream(ctx context.Context, r io.Reader) error {
	events := scanSSE(bufio.NewReaderSize(r, 16<<10))
	for {
		ev, err := events()
		if errors.Is(err, errFrameTooLarge) && (ev.event == "run" || ev.event == "approve") {
			// The stream ends (the rest of the frame cannot be skipped
			// safely), but the command it carried is answered rejected:
			// left queued, Studio would re-send it on every reconnect and
			// the link would tear down on it again until the 30 s ack
			// timer marked it lost — with every command queued behind it.
			if _, ok := l.admit(ev.id); ok {
				l.release(ev.id)
				l.postAck(ack{CommandID: ev.id, State: "rejected",
					Error: fmt.Sprintf("command frame larger than the runtime accepts (%d bytes)", maxFrameBytes)})
			}
		}
		if err != nil {
			return err
		}
		switch ev.event {
		case "ping":
			continue
		case "run":
			var cmd command
			if err := json.Unmarshal(ev.data, &cmd); err != nil {
				slog.Warn("weft/runtime: undecodable run command", "err", err)
				// The frame's id still names a command Studio is waiting
				// on: answer it, once, instead of leaving it to the 30 s
				// lost timer.
				if _, ok := l.admit(ev.id); ok {
					go func(id string) {
						defer l.release(id)
						l.postAck(ack{CommandID: id, State: "rejected", Error: "undecodable command"})
					}(ev.id)
				}
				continue
			}
			if cmd.CommandID == "" {
				cmd.CommandID = ev.id
			}
			if cctx, ok := l.admit(cmd.CommandID); ok {
				go l.dispatch(cctx, cmd)
			}
		case "cancel":
			var c cancelCommand
			if err := json.Unmarshal(ev.data, &c); err != nil || c.CommandID == "" {
				continue
			}
			l.cancelCommand(c.CommandID)
		case "approve":
			var d approvalDecision
			if err := json.Unmarshal(ev.data, &d); err != nil || d.CommandID == "" {
				continue
			}
			if cctx, ok := l.admit(d.CommandID); ok {
				go l.dispatchDecision(cctx, d)
			}
		case "breakpoints":
			var b breakpointsFrame
			if err := json.Unmarshal(ev.data, &b); err != nil {
				continue
			}
			l.setBreakpoints(b.Tools)
		case "steer":
			var st steerFrame
			if err := json.Unmarshal(ev.data, &st); err != nil || st.RunID == "" {
				continue
			}
			l.steer(st)
		case "":
			// A comment or keep-alive line before any event field.
		default:
			slog.Debug("weft/runtime: unknown sse event", "event", ev.event)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

// admit is the at-most-once gate, taken on the stream's own goroutine
// so frames are admitted in the order they arrived: a command id is
// marked seen and given its cancelable context — a cancel frame right
// behind it finds it — exactly once. A repeated id, a frame without
// one (it could not be acked), a stopped link and a link already
// holding maxCommands are all refused; the last is answered rejected.
// The caller owes release(id) when the command's life ends.
func (l *link) admit(id string) (context.Context, bool) {
	if !validCommandID(id) {
		// No id, or one Studio itself would refuse: it could not be
		// acked, and it does not belong in a log line or a metadata
		// value either.
		slog.Warn("weft/runtime: command frame without a usable id ignored")
		return nil, false
	}
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return nil, false
	}
	l.lastID = id
	if l.seen[id] {
		l.mu.Unlock()
		slog.Debug("weft/runtime: repeated command id ignored", "command_id", id)
		return nil, false
	}
	l.seen[id] = true
	l.seenOrder = append(l.seenOrder, id)
	if len(l.seenOrder) > maxSeen {
		delete(l.seen, l.seenOrder[0])
		l.seenOrder = l.seenOrder[1:]
	}
	if len(l.inFlight) >= maxCommands {
		l.runs.Add(1)
		l.mu.Unlock()
		go func() {
			defer l.runs.Done()
			l.postAck(ack{CommandID: id, State: "rejected",
				Error: fmt.Sprintf("runtime busy: %d commands in flight", maxCommands)})
		}()
		return nil, false
	}
	ctx, cancel := context.WithCancel(l.ctx)
	l.inFlight[id] = cancel
	l.runs.Add(1) // under mu and before stopped is set: never races stop's Wait
	l.mu.Unlock()
	return ctx, true
}

// validCommandID is Studio's own rule for a command id (1 to 128
// characters of [A-Za-z0-9._:-]; the minted "cmd_" + ULID is inside
// it), re-checked here: the id is echoed into acks, logs and every
// record of the run (weft.playground.command).
func validCommandID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == ':', c == '-':
		default:
			return false
		}
	}
	return true
}

// release ends an admitted command's life: its context is canceled and
// its in-flight entry dropped (the seen mark stays — at-most-once).
func (l *link) release(id string) {
	l.mu.Lock()
	cancel := l.inFlight[id]
	delete(l.inFlight, id)
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	l.runs.Done()
}

// contain turns a panic on a command's goroutine into that command's
// failure. The loop already contains the agent's own code (tools,
// models, observers); this is the backstop for everything around it —
// a dev tool must never take the program down. accepted says which
// ack the command still owes.
func (l *link) contain(id string, accepted *bool) {
	r := recover()
	if r == nil {
		return
	}
	slog.Error("weft/runtime: command panicked", "command_id", id, "panic", fmt.Sprint(r))
	if *accepted {
		l.postAck(ack{CommandID: id, State: "finished", Status: "failed", Error: "internal error in the runtime (see its log)"})
	} else {
		l.postAck(ack{CommandID: id, State: "rejected", Error: "internal error in the runtime (see its log)"})
	}
}

// slot takes one of the maxRunning run slots, or gives up when the
// command is canceled first.
func (l *link) slot(ctx context.Context) bool {
	select {
	case l.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// dispatch is one admitted command's whole life: re-validation against
// the runtime's own registry, the ack BEFORE execution, the run, the
// finished ack.
func (l *link) dispatch(ctx context.Context, cmd command) {
	defer l.release(cmd.CommandID)
	accepted := false
	defer l.contain(cmd.CommandID, &accepted)

	reason, ok := l.validate(ctx, &cmd)
	switch {
	case !ok:
	case ctx.Err() != nil:
		reason, ok = "canceled before it started", false
	case !l.reserve(cmd):
		reason, ok = "budget_exceeded", false
	}
	if !ok {
		l.postAck(ack{CommandID: cmd.CommandID, State: "rejected", Error: reason})
		return
	}

	// The ack before execution is the at-most-once rule (§5.3): once
	// Studio knows the command is accepted, a replay is refused there
	// (409), and a crash between ack and run shows up as "lost".
	runID := newID("pg_")
	acked := runID
	if cmd.Thread == "fork" {
		// A fork's turn runs under the id its session mints
		// (<session>-tN) once it starts: the finished ack names it. An
		// accepted ack naming runID would hand Studio a run that never
		// exists.
		acked = ""
	}
	l.postAck(ack{CommandID: cmd.CommandID, State: "accepted", RunID: acked})
	accepted = true

	if !l.slot(ctx) {
		// It never ran: its run goes back to the experiment's cap.
		l.unreserve(cmd)
		l.postAck(ack{CommandID: cmd.CommandID, State: "finished", RunID: runID,
			Status: "failed", Error: "canceled before it started"})
		return
	}
	defer func() { <-l.slots }()
	status, finalRun, errText := l.execute(ctx, cmd, runID)
	l.postAck(ack{CommandID: cmd.CommandID, State: "finished", RunID: finalRun, Status: status, Error: errText})
}

// cancelCommand cancels a command this runtime admitted (never the
// app's own runs: it holds no handle to those) — in flight, waiting
// for a run slot, or still validating.
func (l *link) cancelCommand(id string) {
	l.mu.Lock()
	cancel, ok := l.inFlight[id]
	l.mu.Unlock()
	if ok {
		cancel()
		slog.Debug("weft/runtime: command canceled", "command_id", id)
	}
}

// dispatchDecision is one admitted approval decision's whole life: the
// parked-run lookup and the check against its pending calls (the
// runtime's copy is authoritative — a decision for a run it never
// started, one already resumed, or a call that is not parked there is
// rejected, and the park stays), the ack BEFORE resuming, the resume,
// the finished ack.
//
// A parked run resumes once every pending call has its decision, under
// all of them: the core denies an undecided call "no decision", so
// resuming on the first of three would silently refuse the two nobody
// decided. Until then a decision is held — accepted and finished under
// the parked run's own id, which is still the run to look at.
func (l *link) dispatchDecision(ctx context.Context, d approvalDecision) {
	defer l.release(d.CommandID)
	accepted := false
	defer l.contain(d.CommandID, &accepted)

	reject := func(reason string) {
		l.postAck(ack{CommandID: d.CommandID, State: "rejected", Error: reason})
	}
	switch d.Decision {
	case "approve", "deny", "resolve":
	default:
		reject(fmt.Sprintf("unknown decision %q", d.Decision))
		return
	}

	// A fork's park record may have been evicted (maxParked) while its
	// session, still held here, waits on the calls: rebuild it before
	// the lookup. Outside the lock — Pending takes the session's.
	if session, _, err := parseThreadRunID(d.RunID); err == nil {
		l.mu.Lock()
		_, held := l.parked[d.RunID]
		s := l.forks[session]
		l.mu.Unlock()
		if !held && s != nil {
			l.adoptForkParks(s)
		}
	}

	// One step under the lock: find the park, check the call, record
	// the decision, and — when it completes the set — take the park, so
	// two decisions racing on the last call cannot both resume the run
	// (an approved handler would fire twice).
	l.mu.Lock()
	ps := l.parked[d.RunID]
	var reason string
	complete := false
	switch {
	case ps == nil:
		reason = fmt.Sprintf("no parked run %q on this runtime (it may already have been resumed)", d.RunID)
	case !ps.isPending(d.CallID):
		reason = fmt.Sprintf("call %q is not parked on run %s (parked: %s)", d.CallID, d.RunID, ps.pendingIDs())
	case ps.decisions[d.CallID].Decision != "":
		// The held decision stands: a second one — a double click, two
		// people deciding at once — must not silently replace a verdict
		// both were told succeeded.
		reason = fmt.Sprintf("call %q on run %s is already decided (%s); the run resumes once every parked call is",
			d.CallID, d.RunID, ps.decisions[d.CallID].Decision)
	default:
		ps.decisions[d.CallID] = d
		if complete = len(ps.decisions) == len(ps.pending); complete {
			l.forgetParkLocked(d.RunID)
		}
	}
	l.mu.Unlock()
	if reason != "" {
		reject(reason)
		return
	}
	if !complete {
		l.postAck(ack{CommandID: d.CommandID, State: "accepted", RunID: d.RunID})
		l.postAck(ack{CommandID: d.CommandID, State: "finished", RunID: d.RunID, Status: "succeeded"})
		return
	}

	runID := newID("pg_")
	acked := runID
	if ps.sess != nil {
		acked = "" // a fork's resume is its session's next turn (see dispatch)
	}
	l.postAck(ack{CommandID: d.CommandID, State: "accepted", RunID: acked})
	accepted = true

	if !l.slot(ctx) {
		// The resume never started: the park goes back, without this
		// decision, so deciding again resumes it.
		l.restorePark(d.RunID, ps, d.CallID)
		l.postAck(ack{CommandID: d.CommandID, State: "finished", RunID: runID,
			Status: "failed", Error: "canceled before it started; decide again to resume"})
		return
	}
	defer func() { <-l.slots }()
	status, finalRun, errText := l.resume(ctx, ps, runID, d.CommandID)
	if status == "failed" && finalRun == "" {
		// A fork's Decide refused before its session recorded anything
		// (resume reports no run): the boundary is still open, so the
		// park goes back as above.
		l.restorePark(d.RunID, ps, d.CallID)
	}
	l.postAck(ack{CommandID: d.CommandID, State: "finished", RunID: finalRun, Status: status, Error: errText})
}

// lastEventID returns the newest command frame received — the SSE
// resume cursor the reconnect sends as Last-Event-ID. Arrival order,
// not id order: Studio queues by its own sequence, and a caller-chosen
// command id sorts wherever it likes.
func (l *link) lastEventID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastID
}

// postAck is POST /api/runtime/acks. Best effort: a failed ack is
// logged, never retried beyond the next command's own traffic — the
// lost-command state machine on Studio's side is the backstop.
func (l *link) postAck(a ack) {
	if len(a.Error) > maxAckError {
		a.Error = a.Error[:maxAckError] + "…"
	}
	body, err := json.Marshal(a)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.url("/api/runtime/acks"), bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	bearerAuth(req, l.token)
	resp, err := l.client.Do(req)
	if err != nil {
		slog.Warn("weft/runtime: ack failed", "command_id", a.CommandID, "state", a.State, "err", err)
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Warn("weft/runtime: ack refused", "command_id", a.CommandID, "state", a.State, "status", resp.Status)
	}
}

// url resolves a server-relative Studio path against the link's base.
// In-process there is no base: the transport ignores the host, so a
// placeholder origin keeps the URL valid — localhost, the loopback Host
// Studio's DNS-rebinding guard answers without a token, so the
// in-process link needs no exemption of its own.
func (l *link) url(path string) string {
	if l.base != "" {
		return l.base + path
	}
	return "http://localhost" + path
}

// sseEvent is one parsed SSE frame.
type sseEvent struct {
	id    string
	event string
	data  []byte
}

// errFrameTooLarge ends a stream whose frame passed maxFrameBytes.
var errFrameTooLarge = errors.New("weft/runtime: sse frame larger than the link accepts")

// readLine reads one line (its terminator included) of at most limit
// bytes: a peer that never sends the newline cannot grow the buffer
// past it.
func readLine(r *bufio.Reader, limit int) (string, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) > limit {
			return "", errFrameTooLarge
		}
		line = append(line, chunk...)
		if err != bufio.ErrBufferFull {
			return string(line), err
		}
	}
}

// scanSSE returns an iterator over an SSE body's frames: id:, event:
// and data: fields, events separated by a blank line; comment lines
// (:) ignored. Per the SSE spec, multiple data: lines join with \n.
// A frame is bounded (maxFrameBytes); one that is not ends the stream.
func scanSSE(r *bufio.Reader) func() (sseEvent, error) {
	return func() (sseEvent, error) {
		var ev sseEvent
		var data bytes.Buffer
		size := 0
		for {
			line, err := readLine(r, maxFrameBytes-size)
			if errors.Is(err, errFrameTooLarge) {
				// The fields read so far come back with the error: a frame
				// that named its command before the oversized data can
				// still be answered (Studio writes id and event first).
				return sseEvent{id: ev.id, event: ev.event}, err
			}
			size += len(line)
			if line == "" && err != nil {
				if data.Len() > 0 || ev.id != "" || ev.event != "" {
					return ev, nil
				}
				return sseEvent{}, err
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if data.Len() > 0 || ev.event != "" || ev.id != "" {
					ev.data = bytes.TrimSpace(data.Bytes())
					return ev, nil
				}
				size = 0 // a keep-alive blank line: nothing accumulated
			case strings.HasPrefix(line, ":"):
				// comment
			case strings.HasPrefix(line, "id:"):
				ev.id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			case strings.HasPrefix(line, "event:"):
				ev.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data.WriteString(strings.TrimPrefix(line, "data:"))
				data.WriteByte('\n')
			}
			if err != nil {
				if data.Len() > 0 || ev.event != "" || ev.id != "" {
					ev.data = bytes.TrimSpace(data.Bytes())
					return ev, nil
				}
				return sseEvent{}, err
			}
		}
	}
}

// backoff is a small exponential — half a second doubling to a 30
// second cap, each wait jittered over its upper half so a fleet of
// runtimes behind one restarted Studio does not redial in step — that
// starts over once a stream has held (reset). No dependency for a few
// lines of arithmetic.
type backoff struct {
	mu sync.Mutex
	n  int
}

func newBackoff() *backoff { return &backoff{} }

func (b *backoff) next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	d := 30 * time.Second
	if b.n < 6 {
		d = 500 * time.Millisecond * (1 << b.n)
		b.n++
	}
	return d/2 + rand.N(d/2+1)
}

func (b *backoff) reset() {
	b.mu.Lock()
	b.n = 0
	b.mu.Unlock()
}

// breakpointSet lists the stored breakpoint tools, sorted; empty, never
// nil (the registration reports it as [] rather than null).
func (l *link) breakpointSet() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.breakpoints))
	for t := range l.breakpoints {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// setBreakpoints stores the debugger's tool set (§8.3): every run this
// runtime starts from now on parks calls to these tools — the
// breakpoint is rule-driven parking over ADR 0007's boundary, applied
// per run because the agent itself is immutable (D7). The frame
// replaces the set, so the copy Studio re-sends on every reconnect is
// a no-op; an empty set clears.
func (l *link) setBreakpoints(tools []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.breakpoints = map[string]bool{}
	for _, t := range tools {
		if t != "" {
			l.breakpoints[t] = true
		}
	}
	slog.Debug("weft/runtime: breakpoints set", "tools", tools)
}

// forkSteer is a fork turn in flight, as the steer registry holds it:
// the fork's session and the run options the turn was sent with (the
// command's shaping, the park rule above all).
type forkSteer struct {
	sess *thread.Session
	opts []core.RunOption
}

// steer delivers one user message into a runtime-started run (§8.4):
// the ephemeral run's steering queue (core.Steering's source drains it
// at the loop's two fixed points), or a fork turn's session as a
// thread steer. The app's own turns are never steerable from here
// (PQ7): this link holds no handle to them.
func (l *link) steer(st steerFrame) {
	l.mu.Lock()
	q := l.steerQ[st.RunID]
	fs, isFork := l.steerSess[st.RunID]
	l.mu.Unlock()
	switch {
	case q != nil:
		select {
		case q <- core.User(st.Message):
		default:
			slog.Debug("weft/runtime: steer dropped (queue full or run ending)", "run_id", st.RunID)
		}
	case isFork:
		// A thread steer joins the fork's running turn, or — when it
		// cannot (the approval boundary, a StopWhen end, past the last
		// drain point) — becomes a follow-up turn that thread runs
		// under the aimed turn's run options (thread ≥ the steer
		// follow-up fix), so the park rule still binds it. The turn's
		// options ride this Send too: a steer that lands as the turn
		// ends, with nothing in flight and no boundary open, runs as a
		// plain turn under its own options alone — it must not run
		// unparked (§6 rule 3).
		if _, err := fs.sess.Send(context.Background(), core.User(st.Message),
			thread.As(thread.Steer), thread.RunOptions(fs.opts...)); err != nil {
			slog.Debug("weft/runtime: steer into the fork refused", "run_id", st.RunID, "err", err)
		}
	default:
		slog.Debug("weft/runtime: steer for a run not in flight here", "run_id", st.RunID)
	}
}
