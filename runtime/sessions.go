package runtime

// The debugger's verbs on the app's own turns (plan F4.1): a `session`
// frame names a turn of one of the app's thread sessions — by public
// id, session id and run id — and the runtime acts through the Session
// the app's SessionResolver answers. Studio never runs the agent and
// never writes the session: the app's Session does, in the app's
// process, under the writer lease (thread's T1).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/thread"
)

// The session frame's verbs.
const (
	verbDecide = "decide"
	verbSteer  = "steer"
	verbCancel = "cancel"
)

// The refusals a session command can carry, word for word: Studio and
// the UIs show them as they are (the locked one is the one-line fix).
const (
	refusedLocked    = "the app is writing this session; fork instead"
	refusedNoSession = "no session for this public id"
	refusedNoTurn    = "no running turn to steer"
	refusedCancel    = "cancel is not supported on an app turn"
	refusedNoOption  = "this runtime does not act on the app's sessions: runtime.Sessions(...) is not set"
	refusedSigned    = "this session requires signed decisions: decide it in the app (thread.DecideSigned)"
)

// skippedReason is the deny reason a decision without one carries — the
// text the model sees after "DENIED: ". One default for every deny the
// devtools panel sends: a runtime-started run's (executor.go) and an app
// turn's.
const skippedReason = "skipped from the devtools panel"

// sessionResolveTimeout bounds one SessionResolver call, as
// resolveTimeout bounds a ModelResolver's: the resolve runs before the
// ack, inside Studio's ack window. A var so a test can shorten it.
var sessionResolveTimeout = 10 * time.Second

// dispatchSession is one admitted session command's whole life:
// validation, the Session resolved and its lease claimed, the
// verb-specific check, the ack BEFORE the Session is written, the
// write, the finished ack, then the resolver's release. A refusal
// before the ack writes nothing; the at-most-once rule is the run
// command's (an accepted ack Studio did not take runs nothing).
func (l *link) dispatchSession(ctx context.Context, sc sessionCommand) {
	defer l.release(sc.CommandID)
	accepted := false
	defer l.contain(sc.CommandID, &accepted)

	reject := func(reason string) {
		l.postAck(ack{CommandID: sc.CommandID, State: "rejected", Error: reason})
	}
	if reason := checkSessionCommand(sc); reason != "" {
		reject(reason)
		return
	}
	if l.cfg.sessions == nil {
		reject(refusedNoOption)
		return
	}
	if sc.Verb == verbCancel {
		// thread holds no per-turn cancel: Interrupt cancels a turn only
		// to run a message in its place, and a debugger's cancel must not
		// start a turn. Refused before anything is resolved.
		reject(refusedCancel)
		return
	}

	s, done, reason := l.resolveSession(ctx, sc)
	if reason != "" {
		reject(reason)
		return
	}
	defer done() // after the finished ack: a closed Session waits its turn out

	switch sc.Verb {
	case verbDecide:
		if s.RequiresSigned() {
			// thread refuses an unsigned Decide at its door: say so
			// before the ack, not as a failure after it.
			reject(refusedSigned)
			return
		}
		if reason := checkPending(s, sc); reason != "" {
			reject(reason)
			return
		}
	}
	// A steer has no check here: whether a turn is running is only true
	// under the session's lock, so Session.Steer decides it atomically
	// after the ack — ErrNotRunning is the finished/failed "no running
	// turn to steer", and nothing was written.

	if res := l.postAck(ack{CommandID: sc.CommandID, State: "accepted", RunID: sc.RunID}); res != ackTaken {
		l.settleUnconfirmed(sc.CommandID, res, notConfirmed)
		slog.Warn("weft/runtime: the accepted ack was not taken; the session is not written",
			"command_id", sc.CommandID)
		return
	}
	accepted = true
	stopBeat := l.heartbeat(sc.CommandID, sc.RunID) // stopped before the finished ack (see dispatch)
	defer stopBeat()

	// The app's turn outlives the command: a resumed turn or a steer's
	// delivery runs on the app's Session, never under the command's
	// context (released when this returns) nor the link's.
	actx := context.WithoutCancel(ctx)
	finalRun, err := sc.RunID, error(nil)
	switch sc.Verb {
	case verbDecide:
		d := decisionOf(sc)
		var turn *thread.Turn
		turn, err = s.Decide(actx, d)
		if turn != nil {
			finalRun = turn.RunID() // the resume: the session's next turn, the app's run
		}
	case verbSteer:
		// Delivered only into the turn running now; never a turn of its
		// own (thread.ErrNotRunning otherwise, nothing written).
		err = s.Steer(actx, core.User(sc.Message))
	}
	stopBeat()
	if err != nil {
		text := err.Error()
		switch {
		case errors.Is(err, thread.ErrLocked):
			// Claimed above, lost since (another writer took the session
			// in between): nothing was written — T1 — and the words are
			// the refusal's.
			text = refusedLocked
		case errors.Is(err, thread.ErrNotRunning):
			text = fmt.Sprintf("%s (session %s is not running a turn)", refusedNoTurn, sc.SessionID)
		}
		slog.Warn("weft/runtime: session command failed", "command_id", sc.CommandID,
			"session", sc.SessionID, "verb", sc.Verb, "err", err)
		l.postAck(ack{CommandID: sc.CommandID, State: "finished", RunID: sc.RunID, Status: "failed", Error: text})
		return
	}
	l.postAck(ack{CommandID: sc.CommandID, State: "finished", RunID: finalRun, Status: "succeeded"})
}

// checkSessionCommand is the frame's own validation: a known verb with
// what it needs.
func checkSessionCommand(sc sessionCommand) string {
	switch sc.Verb {
	case verbDecide, verbSteer, verbCancel:
	default:
		return fmt.Sprintf("unknown session verb %q", sc.Verb)
	}
	if sc.PublicID == "" || sc.SessionID == "" || sc.RunID == "" {
		return "a session command needs public_id, session_id and run_id"
	}
	switch sc.Verb {
	case verbDecide:
		switch sc.Decision {
		case "approve", "deny", "resolve":
		default:
			return fmt.Sprintf("unknown decision %q", sc.Decision)
		}
		if sc.CallID == "" {
			return "a decision needs a call_id"
		}
	case verbSteer:
		if sc.Message == "" {
			return "a steer needs a message"
		}
	}
	return ""
}

// decisionOf turns the frame's decision into thread's.
func decisionOf(sc sessionCommand) thread.Decision {
	var d thread.Decision
	switch sc.Decision {
	case "approve":
		d = thread.Approve(sc.CallID)
	case "deny":
		d = thread.Deny(sc.CallID, orDefault(sc.Reason, skippedReason))
	default: // resolve
		d = thread.Resolve(sc.CallID, sc.Content)
	}
	d.Who = sc.Actor
	return d
}

// resolveSession asks the app's resolver for the Session behind the
// public id and claims its writer lease, so the ack is only ever sent
// for a Session that can write. ErrLocked — from the resolver or the
// claim — is refusedLocked; ErrStale releases and resolves once more.
// done is the resolver's release (never nil); reason is non-empty when
// the command is refused, everything released.
func (l *link) resolveSession(ctx context.Context, sc sessionCommand) (s *thread.Session, done func(), reason string) {
	for attempt := 0; ; attempt++ {
		s, release, err := l.callResolver(ctx, sc.PublicID)
		if release == nil {
			release = func() {}
		}
		if err == nil && s == nil {
			release()
			return nil, nil, refusedNoSession
		}
		if err == nil && s.ID() != sc.SessionID {
			release()
			return nil, nil, fmt.Sprintf("the session resolver answered session %s for public id %s, not the run's session %s",
				s.ID(), sc.PublicID, sc.SessionID)
		}
		if err == nil {
			// Bounded like the resolve: both run before the ack, inside
			// Studio's ack window.
			cctx, cancel := context.WithTimeout(ctx, sessionResolveTimeout)
			err = s.Claim(cctx)
			cancel()
		}
		switch {
		case err == nil:
			return s, release, ""
		case errors.Is(err, thread.ErrLocked):
			release()
			return nil, nil, refusedLocked
		case errors.Is(err, thread.ErrStale) && attempt == 0:
			release() // the view fell behind: open it again, once
			continue
		default:
			release()
			return nil, nil, err.Error()
		}
	}
}

// errResolverTimeout is a resolver call that outlived its bound.
var errResolverTimeout = errors.New("session resolver timed out")

// callResolver is one bounded SessionResolver call (resolveModel's
// shape): canceled after sessionResolveTimeout, abandoned at the bound
// — a late answer's release is called, so a Session it opened is not
// left holding anything — and a panic is the call's error.
func (l *link) callResolver(ctx context.Context, publicID string) (*thread.Session, func(), error) {
	rctx, cancel := context.WithTimeout(ctx, sessionResolveTimeout)
	defer cancel()
	type result struct {
		s       *thread.Session
		release func()
		err     error
	}
	// Unbuffered: the answer is handed over only while this call still
	// waits; once abandoned, the goroutine releases it itself.
	out := make(chan result)
	abandoned := make(chan struct{})
	go func() {
		r := result{}
		func() {
			defer func() {
				if p := recover(); p != nil {
					r = result{err: fmt.Errorf("session resolver panicked: %v", p)}
				}
			}()
			r.s, r.release, r.err = l.cfg.sessions(rctx, publicID)
		}()
		select {
		case out <- r:
		case <-abandoned:
			if r.release != nil {
				r.release()
			}
		}
	}()
	select {
	case r := <-out:
		return r.s, r.release, r.err
	case <-rctx.Done():
		close(abandoned)
		if errors.Is(rctx.Err(), context.DeadlineExceeded) {
			return nil, nil, errResolverTimeout
		}
		return nil, nil, rctx.Err()
	}
}

// checkPending refuses a decision for a call that is not parked on the
// session's open boundary, or that another run parked: the session's
// own Pending is authoritative.
func checkPending(s *thread.Session, sc sessionCommand) string {
	pending := s.Pending()
	ids := make([]string, 0, len(pending))
	for _, r := range pending {
		if r.CallID == sc.CallID {
			if r.RunID != "" && r.RunID != sc.RunID {
				return fmt.Sprintf("call %q was parked by run %s, not %s", sc.CallID, r.RunID, sc.RunID)
			}
			return ""
		}
		ids = append(ids, r.CallID)
	}
	if len(ids) == 0 {
		return fmt.Sprintf("session %s has no parked call: call %q is not pending", sc.SessionID, sc.CallID)
	}
	return fmt.Sprintf("call %q is not pending on session %s (pending: %s)", sc.CallID, sc.SessionID, strings.Join(ids, ", "))
}
