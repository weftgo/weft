package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
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

	id string // this runtime's id, stable for the link's lifetime

	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	seen     map[string]bool // command ids already acked (at-most-once)
	inFlight map[string]context.CancelFunc
	tally    map[string]*budgetState // per experiment_id
	parked   map[string]*parkState   // run id → the parked run this runtime started

	reconnect func() time.Duration // backoff; indirected by tests
}

// newLink wires the client. In-process when cfg.local is set (no
// socket), else a plain HTTP client on url.
func newLink(c *config, reg *registry, url, token string) *link {
	l := &link{
		cfg:      c,
		reg:      reg,
		token:    token,
		id:       newID("rt_"),
		done:     make(chan struct{}),
		seen:     map[string]bool{},
		inFlight: map[string]context.CancelFunc{},
		tally:    map[string]*budgetState{},
		parked:   map[string]*parkState{},
	}
	if c.local != nil {
		l.client = inProcessClient(c.local)
	} else {
		l.base = strings.TrimRight(url, "/")
		l.client = &http.Client{Timeout: 0} // the SSE stream is long-lived
	}
	bo := newBackoff()
	l.reconnect = bo.next
	return l
}

// start runs the connect loop on its own goroutine. It returns an
// error only if the link cannot even construct its request (never in
// practice); every network failure is retried with backoff — a dev
// tool must not fail the program (§6 rule 1's spirit).
func (l *link) start() error {
	var ctx context.Context
	ctx, l.cancel = context.WithCancel(context.Background())
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

// stop shuts the link down and waits for it.
func (l *link) stop() {
	if l.cancel != nil {
		l.cancel()
		select {
		case <-l.done:
		case <-time.After(5 * time.Second):
		}
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
	slog.Debug("weft/runtime: command stream ended; reconnecting", "err", err)
	return err
}

// register is POST /api/runtime/register — on connect and again after
// every reconnect, so Studio's copy of the agents, allow-lists and
// caps is always the runtime's own (§10.3).
func (l *link) register(ctx context.Context) error {
	body, err := json.Marshal(l.reg.registration(l.id))
	if err != nil {
		return err
	}
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
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("weft/runtime: register: %s", resp.Status)
	}
	var out registerResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
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
	url := l.url("/api/runtime/commands?runtime=" + l.id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("weft/runtime: commands: %s", resp.Status)
	}
	return l.readStream(ctx, resp.Body)
}

// readStream parses the SSE frames and dispatches them. The frame ids
// are command ids; the newest one seen is the resume cursor.
func (l *link) readStream(ctx context.Context, r io.Reader) error {
	events := scanSSE(bufio.NewReaderSize(r, 16<<10))
	for {
		ev, err := events()
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
				continue
			}
			if cmd.CommandID == "" && ev.id != "" {
				cmd.CommandID = ev.id
			}
			go l.dispatch(cmd)
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
			go l.dispatchDecision(d)
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

// dispatch is one command's whole life: at-most-once check,
// re-validation against the runtime's own registry, the ack BEFORE
// execution, the run, the finished ack.
func (l *link) dispatch(cmd command) {
	l.mu.Lock()
	if l.seen[cmd.CommandID] {
		l.mu.Unlock()
		slog.Debug("weft/runtime: repeated command id ignored", "command_id", cmd.CommandID)
		return
	}
	l.seen[cmd.CommandID] = true
	l.mu.Unlock()

	runID := newID("pg_")
	if reason, ok := l.validate(cmd); !ok {
		l.postAck(ack{CommandID: cmd.CommandID, State: "rejected", Error: reason})
		return
	}

	// The ack before execution is the at-most-once rule (§5.3): once
	// Studio knows the command is accepted, a replay is refused there
	// (409), and a crash between ack and run shows up as "lost".
	l.postAck(ack{CommandID: cmd.CommandID, State: "accepted", RunID: runID})

	ctx, cancel := context.WithCancel(context.Background())
	l.mu.Lock()
	l.inFlight[cmd.CommandID] = cancel
	l.mu.Unlock()
	defer func() {
		cancel()
		l.mu.Lock()
		delete(l.inFlight, cmd.CommandID)
		l.mu.Unlock()
	}()

	status, finalRun := l.execute(ctx, cmd, runID)
	l.postAck(ack{CommandID: cmd.CommandID, State: "finished", RunID: finalRun, Status: status})
}

// cancelCommand cancels a run this runtime started (never the app's
// own: it holds no handle to those). A command still queued in
// dispatch order simply never started — dropping the seen mark would
// violate at-most-once, so the cancel only reaches in-flight runs.
func (l *link) cancelCommand(id string) {
	l.mu.Lock()
	cancel, ok := l.inFlight[id]
	l.mu.Unlock()
	if ok {
		cancel()
		slog.Debug("weft/runtime: command canceled", "command_id", id)
	}
}

// dispatchDecision is one approval decision's whole life: the
// at-most-once check, the parkState lookup (its copy is authoritative
// — a decision for a run it never started, or one already resumed, is
// rejected), the ack BEFORE resuming, the resume, the finished ack.
func (l *link) dispatchDecision(d approvalDecision) {
	l.mu.Lock()
	if l.seen[d.CommandID] {
		l.mu.Unlock()
		return
	}
	l.seen[d.CommandID] = true
	ps, ok := l.parked[d.RunID]
	l.mu.Unlock()
	if !ok || ps == nil {
		l.postAck(ack{CommandID: d.CommandID, State: "rejected",
			Error: "no parked run " + d.RunID + " on this runtime (it may already have been resumed)"})
		return
	}
	switch d.Decision {
	case "approve", "deny", "resolve":
	default:
		l.postAck(ack{CommandID: d.CommandID, State: "rejected",
			Error: "unknown decision " + d.Decision})
		return
	}

	runID := newID("pg_")
	l.postAck(ack{CommandID: d.CommandID, State: "accepted", RunID: runID})

	ctx, cancel := context.WithCancel(context.Background())
	l.mu.Lock()
	l.inFlight[d.CommandID] = cancel
	l.mu.Unlock()
	defer func() {
		cancel()
		l.mu.Lock()
		delete(l.inFlight, d.CommandID)
		l.mu.Unlock()
	}()

	status, finalRun := l.resume(ctx, ps, d, runID)
	l.postAck(ack{CommandID: d.CommandID, State: "finished", RunID: finalRun, Status: status})
}

// lastEventID returns the newest command id seen — the SSE resume
// cursor the reconnect sends as Last-Event-ID.
func (l *link) lastEventID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastEventIDLocked()
}

func (l *link) lastEventIDLocked() string {
	var newest string
	// seen keys are command ids; ULIDs sort by creation time, so the
	// lexicographically largest is the newest.
	for id := range l.seen {
		if id > newest {
			newest = id
		}
	}
	return newest
}

// postAck is POST /api/runtime/acks. Best effort: a failed ack is
// logged, never retried beyond the next command's own traffic — the
// lost-command state machine on Studio's side is the backstop.
func (l *link) postAck(a ack) {
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
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Warn("weft/runtime: ack refused", "command_id", a.CommandID, "state", a.State, "status", resp.Status)
	}
}

// url resolves a server-relative Studio path against the link's base.
// In-process there is no base: the transport ignores the host, so a
// dummy origin keeps the URL valid.
func (l *link) url(path string) string {
	if l.base != "" {
		return l.base + path
	}
	return "http://weft.studio.local" + path
}

// sseEvent is one parsed SSE frame.
type sseEvent struct {
	id    string
	event string
	data  []byte
}

// scanSSE returns an iterator over an SSE body's frames: id:, event:
// and data: fields, events separated by a blank line; comment lines
// (:) ignored. Per the SSE spec, multiple data: lines join with \n.
func scanSSE(r *bufio.Reader) func() (sseEvent, error) {
	return func() (sseEvent, error) {
		var ev sseEvent
		var data bytes.Buffer
		for {
			line, err := r.ReadString('\n')
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
// second cap. No dependency for four lines of arithmetic.
type backoff struct {
	n int
}

func newBackoff() *backoff { return &backoff{} }

func (b *backoff) next() time.Duration {
	b.n++
	if b.n > 6 {
		return 30 * time.Second
	}
	d := 500 * time.Millisecond * (1 << (b.n - 1))
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}
