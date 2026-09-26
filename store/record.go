package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"maps"
	"runtime/debug"
	"sync"
	"time"

	"github.com/weftgo/weft"
)

// RecordOption configures a Record tap.
type RecordOption interface {
	applyRecord(*recorder)
}

type tagsOption map[string]string

func (o tagsOption) applyRecord(r *recorder) {
	maps.Copy(r.tags, o)
}

// Tags attaches consumer-supplied pairs to every record this tap
// writes — hmm: {"cwd": wd}, Weft CI: {"pr": n}. Query.Tags matches a
// record when every queried pair matches; a record may carry more.
// Several Tags options merge.
func Tags(kv map[string]string) RecordOption { return tagsOption(kv) }

// Record returns a weft.Option that records every run of the agent it
// is installed on into s — install it on every agent of a fleet, since
// a subagent's child records itself (see below).
//
//   - The row is written at RunStart: running, with the identity ADR
//     0010 defines — agent name and model from the event, the manifest
//     hash (sha256 of weft.Manifest(agent), "" when unnamed), the core
//     version, and the Tags. A crash leaves this row plus whatever
//     events landed: evidence, not silence.
//   - Every event is one Append as it arrives, Nested inline — nothing
//     is buffered, so a crash loses nothing that was emitted and a
//     reader can tail a live run. The append runs synchronously on the
//     emitting goroutine (pi's cadence; both surveyed paces work at
//     agent event rates) and bumps the heartbeat; a quiet-but-alive
//     run (a long tool call) is kept alive by a ticker at a third of
//     HeartbeatTimeout, so a slow tool never reads as a crash.
//   - The closing write goes through OnRunEnd — the failure signal a
//     tap cannot carry, because a failed run emits nothing after its
//     last delivered event (ADR 0004). On success: succeeded, the
//     result, the finish time. On failure: failed, the RunError's
//     partial transcript as the result, and its text in Err.
//
// Child runs: a subagent's child records itself. Its tap sees its own
// unwrapped events; its OnRunEnd fires inside the parent's tool call
// and closes its record with its own result; and the parent's tool
// call rides the child's context, so the child's row learns ParentID
// and ParentCallID from weft.CallFromContext — no run-id parsing. The
// parent's own stream keeps the child's events inline as Nested, so
// the parent's record replays as one stream (ADR 0004's order is the
// record's order). One writer per run id: the parent never writes the
// child's rows.
//
// Store errors are logged through the agent's logger and never fail
// the run — observation is not the loop's business (ADR 0006). After a
// failed write the record is marked degraded and the closing write
// carries Err prefixed with the store's error, so a gap is visible in
// the record, not only in the log. Record never retries a write: an
// event lost by a failed append stays lost, and the record says so.
func Record(s Store, opts ...RecordOption) weft.Option {
	r := &recorder{
		store:          s,
		tags:           map[string]string{},
		heartbeatEvery: HeartbeatTimeout / 3,
		now:            time.Now,
	}
	for _, o := range opts {
		if o != nil {
			o.applyRecord(r)
		}
	}
	return weft.Options(weft.Tap(r.tap), weft.OnRunEnd(r.end))
}

// recorder is the shared state of one Record option value: per-run
// logs keyed by run id, the manifest-hash cache, and the knobs the
// tests shrink (heartbeat cadence, clock). Safe for concurrent use —
// taps fire on the loop's goroutine and, for Nested, on the
// delegating tool's goroutine in parallel steps.
type recorder struct {
	store          Store
	tags           map[string]string
	heartbeatEvery time.Duration
	now            func() time.Time

	mu     sync.Mutex
	logs   map[string]*runLog
	hashes map[*weft.Agent]string
}

// runLog is one open run's recording state.
type runLog struct {
	rec      RunRecord
	logger   *slog.Logger
	degraded string // the first store error; "" while healthy
	stopped  bool   // ticker stopped; the run's recording is closed
	stop     chan struct{}
}

func (r *recorder) tap(ctx context.Context, ev weft.Event) {
	id, _ := eventRunID(ev)
	if id == "" {
		return // nothing attributable; no event type in the core is unattributed
	}
	if _, ok := ev.(weft.RunStart); ok {
		r.start(ctx, ev.(weft.RunStart))
		return
	}
	r.mu.Lock()
	l := r.logs[id]
	r.mu.Unlock()
	if l == nil {
		return
	}
	if err := r.store.Append(context.Background(), id, ev); err != nil {
		r.degrade(l, "append", err)
		return
	}
	r.mu.Lock()
	l.rec.Heartbeat = r.now()
	r.mu.Unlock()
}

// start writes the RunStart row — the crash evidence — with the
// identity the recorder can derive: the agent running on ctx names the
// manifest hash and the logger, and the parent's tool call (a child
// run's context carries it) supplies the tree linkage.
func (r *recorder) start(ctx context.Context, e weft.RunStart) {
	var (
		parentID, parentCallID string
		hash, weftVer          string
		logger                 *slog.Logger
	)
	if c, ok := weft.CallFromContext(ctx); ok && c.RunID != "" && c.RunID != e.ID {
		parentID, parentCallID = c.RunID, c.CallID
	}
	if a := weft.AgentFromContext(ctx); a != nil {
		hash = r.hashFor(a)
		logger = a.Logger()
	}
	weftVer = weftVersion()

	now := r.now()
	l := &runLog{stop: make(chan struct{})}
	l.rec = RunRecord{
		ID:           e.ID,
		ParentID:     parentID,
		ParentCallID: parentCallID,
		Agent:        e.Agent,
		Model:        e.Model,
		ManifestHash: hash,
		WeftVersion:  weftVer,
		Started:      now,
		Heartbeat:    now,
		Status:       Running,
		Tags:         cloneTags(r.tags),
	}
	l.logger = logger
	r.mu.Lock()
	if r.logs == nil {
		r.logs = map[string]*runLog{}
	}
	r.logs[e.ID] = l
	r.mu.Unlock()
	if err := r.store.Save(context.Background(), l.rec); err != nil {
		r.degrade(l, "save", err)
	}
	// The stream holds every event the run emitted, RunStart included:
	// the row is the identity, the first stream row is the event.
	if err := r.store.Append(context.Background(), e.ID, e); err != nil {
		r.degrade(l, "append", err)
	}
	r.startTicker(ctx, l)
}

// end is the OnRunEnd half: the closing write, with the outcome no
// event carries. It uses a background context — the run's own context
// is often cancelled by the very failure being recorded.
func (r *recorder) end(_ context.Context, res *weft.RunResult, err error) {
	r.mu.Lock()
	l := r.logs[res.ID]
	delete(r.logs, res.ID)
	if l != nil && !l.stopped {
		l.stopped = true
		close(l.stop)
	}
	r.mu.Unlock()
	if l == nil {
		return // never started (no RunStart observed); nothing to close
	}
	now := r.now()
	l.rec.Finished = now
	l.rec.Heartbeat = now
	l.rec.Result = res
	if err != nil {
		l.rec.Status = Failed
		l.rec.Err = err.Error()
	} else {
		l.rec.Status = Succeeded
	}
	if l.degraded != "" {
		if l.rec.Err != "" {
			l.rec.Err = "store degraded: " + l.degraded + "; " + l.rec.Err
		} else {
			l.rec.Err = "store degraded: " + l.degraded
		}
	}
	if serr := r.store.Save(context.Background(), l.rec); serr != nil {
		r.degrade(l, "save", serr)
	}
}

// startTicker keeps a quiet run's heartbeat fresh: one tick every
// heartbeatEvery (HeartbeatTimeout / 3), stopped at run end and when
// the run's context dies — the backstop for a run whose end the
// recorder never observes.
func (r *recorder) startTicker(ctx context.Context, l *runLog) {
	if r.heartbeatEvery <= 0 {
		return
	}
	go func() {
		t := time.NewTicker(r.heartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				r.touch(l)
			case <-l.stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// touch re-saves the row with a fresh heartbeat.
func (r *recorder) touch(l *runLog) {
	r.mu.Lock()
	if l.stopped {
		r.mu.Unlock()
		return
	}
	rec := l.rec
	rec.Heartbeat = r.now()
	r.mu.Unlock()
	if err := r.store.Save(context.Background(), rec); err != nil {
		r.degrade(l, "save", err)
	}
}

// degrade records the first store error and logs it through the
// agent's logger — a gap must be visible in the record and in the log.
func (r *recorder) degrade(l *runLog, op string, err error) {
	r.mu.Lock()
	if l.degraded == "" {
		l.degraded = err.Error()
	}
	logger := l.logger
	r.mu.Unlock()
	if logger == nil {
		logger = slog.Default()
	}
	logger.Error("store: "+op+" failed", "run", l.rec.ID, "err", err)
}

// hashFor caches sha256(weft.Manifest(agent)) per agent: the agent is
// immutable after New, so one hash serves every run. Unnamed agents
// have no manifest — "" is the recorded value.
func (r *recorder) hashFor(a *weft.Agent) string {
	r.mu.Lock()
	if h, ok := r.hashes[a]; ok {
		r.mu.Unlock()
		return h
	}
	r.mu.Unlock()
	h := ""
	if b, err := weft.Manifest(a); err == nil {
		sum := sha256.Sum256(b)
		h = hex.EncodeToString(sum[:])
	}
	r.mu.Lock()
	if r.hashes == nil {
		r.hashes = map[*weft.Agent]string{}
	}
	r.hashes[a] = h
	r.mu.Unlock()
	return h
}

// eventRunID returns the run an event attributes itself to.
func eventRunID(ev weft.Event) (string, bool) {
	switch e := ev.(type) {
	case weft.RunStart:
		return e.ID, true
	case weft.StepStart:
		return e.RunID, true
	case weft.TextDelta:
		return e.RunID, true
	case weft.ReasoningDelta:
		return e.RunID, true
	case weft.ToolArgsDelta:
		return e.RunID, true
	case weft.ToolStart:
		return e.RunID, true
	case weft.ToolFinish:
		return e.RunID, true
	case weft.StepFinish:
		return e.RunID, true
	case weft.RunFinish:
		return e.RunID, true
	case weft.Nested:
		return e.RunID, true
	}
	return "", false
}

// weftVersion reports the core module version this process built
// against, from the build info: "(devel)" inside the weft workspace,
// the tagged version in a consumer's build. Best effort — the string
// is recorded provenance, not a gate.
func weftVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, dep := range bi.Deps {
		if dep.Path != "github.com/weftgo/weft" {
			continue
		}
		if dep.Replace != nil && dep.Replace.Version != "" {
			return dep.Replace.Version
		}
		if dep.Version != "" {
			return dep.Version
		}
	}
	// A workspace build (go.work) resolves the core from disk: the dep
	// carries no version, and "(devel)" is the honest answer.
	return "(devel)"
}
