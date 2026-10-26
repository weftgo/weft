package runtime

import (
	"context"
	"reflect"
	"sort"
	"sync"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/internal/linkreg"
)

// Breakpoints makes the debugger's breakpoints (§8.3) park tools on the
// runs the app starts itself — plain Generate/Stream calls and weft/
// thread turns alike — not only on the runs this package starts (plan
// F4, PQ7). Pass it to core.New (weft.New) with the agent's other
// options:
//
//	agt := weft.New(model, tools…, runtime.Breakpoints())
//	defer runtime.Install(runtime.Agents(agt), runtime.Sessions(resolve))()
//
// It is a core.ParkSource (ADR 0030) bound to the process's runtime
// link lazily: the agent is built before Install runs, so each run
// reads, once at its start, the breakpoint set of the links Install
// has started and not yet stopped — the set Studio's PUT
// /api/runtimes/{id}/breakpoints replaces and re-sends at every
// registration. One link per process is the rule; should several be
// live, a run parks on the union of their sets. A call to a breakpoint
// tool parks at the approval boundary (ADR 0007) as a RequireApproval
// tool's does: its ToolStart and no ToolFinish, the run ending
// successfully with the call on RunResult.Pending. In a Subagent's
// child run (which inherits the parent's context) the child parks and
// the parent's call to it is a SUBAGENT_PENDING result the parent
// model sees and continues past — core's rule for any park in a child,
// not a park of the parent. Under a thread turn with Sessions and a
// public id, Studio accepts a decision on such a child's run (it
// inherits the session id and public id) and answers 202, but the
// runtime then rejects it ("no parked call": the session's Pending is
// empty, the parent turn having completed) — the child's park is
// decided by nobody, and the parent has continued past it.
//
// Who decides a parked app run:
//
//   - a thread turn (weft/thread) parks on a breakpoint only when this
//     process set runtime.Sessions and the session has a public id
//     (thread.PublicID) — Studio decides by public id, and a park
//     nobody could decide would hold the session (every later Send
//     queues behind it, ADR 0021 §5) until the app decides it; without
//     both the source answers nothing for session runs (those carrying
//     weft.session.id). With both the debugger's approve / deny /
//     resolve reaches the turn through the app's own Session (Decide),
//     which resumes it;
//   - a plain Generate ends with res.Pending, and only the app can
//     resume it (Generate again with Messages(res.Messages...) and
//     weft.Approve / Deny / Resolve): Studio decides session turns
//     only (Sessions resolves by public id), so the run page shows such
//     a run parked, read-only.
//
// The set is process-wide: every agent built with the option parks on
// it, whether or not it was given to Agents — the registration's
// breakpoints_own reports only the agents given to Agents. A link
// whose stream dropped but that was not stopped keeps its last set: the
// app's runs keep parking on it until the link reconnects and Studio
// re-sends the set (or the link stops), and a decision reaches a
// parked turn once it reconnects. A link STOPPED while a turn is
// already parked leaves that park with no decider until the app
// decides it or a link with Sessions comes back: the park set is read
// once per run, at its start, and the stop does not unpark what it
// parked. The runs this runtime starts
// itself — playground runs and the runs started inside them, which
// carry weft.playground metadata — are left to their own breakpoint
// rule (a ParkOn filtered to the agent's tools, on their override
// fingerprint): the source answers nothing for them.
//
// The option is inert without a link: with no Install, or Install not
// enabled (WEFT_ENV not dev, no Enabled(true)), or a link stopped, or
// no breakpoint set, the source answers nothing and the run is
// byte-identical to one built without it. The registration reports
// breakpoints_own: true when any agent given to Agents carries it.
func Breakpoints() core.Option { return core.ParkSource(breakpointSource) }

// playgroundKey is the metadata key every run this runtime starts
// carries (runOptions); the source skips such runs.
const playgroundKey = "weft.playground"

// sessionKey is the metadata key a weft/thread turn carries, and
// publicIDKey the one a turn of a session with thread.PublicID carries:
// the source parks a session run only when a live link has Sessions
// and the run has a public id.
const (
	sessionKey  = "weft.session.id"
	publicIDKey = "weft.public_id"
)

// liveLinks is the process's runtime links Breakpoints reads: a link
// joins at Install (before it dials) and leaves at its stop. The
// process's embedded Studio reads the same set through linkreg (its
// /api/meta runtime_link, U20.2): in-process to it, or the Studio the
// link dialled instead.
var liveLinks struct {
	mu    sync.Mutex
	links map[*link]func() // the link → its linkreg removal
}

func addLiveLink(l *link) {
	entry := &linkreg.Link{StudioURL: dialledURL(l.base)}
	if l.cfg != nil && l.cfg.local != nil {
		entry.Local = l.cfg.local
	}
	liveLinks.mu.Lock()
	defer liveLinks.mu.Unlock()
	if liveLinks.links == nil {
		liveLinks.links = map[*link]func(){}
	}
	if _, ok := liveLinks.links[l]; !ok {
		liveLinks.links[l] = linkreg.Add(entry)
	}
}

func removeLiveLink(l *link) {
	liveLinks.mu.Lock()
	defer liveLinks.mu.Unlock()
	if remove, ok := liveLinks.links[l]; ok {
		remove()
		delete(liveLinks.links, l)
	}
}

// breakpointSource is the ParkSource behind Breakpoints: the union of
// the live links' breakpoint sets, sorted, nil when there is none. It
// takes two mutexes and copies a small map — prompt, as ParkSource
// asks. A top-level function, so an agent's ParkSources can be
// recognised as carrying it (carriesBreakpoints).
func breakpointSource(ctx context.Context) []string {
	// A run this runtime started (or one started inside it — a Subagent
	// child inherits the metadata) already carries its breakpoints as
	// its own filtered ParkOn, written into its override fingerprint;
	// the source adds nothing there, so the fingerprint alone decides
	// what a playground run parks (ADR 0030).
	md := core.MetadataFromContext(ctx)
	if md[playgroundKey] == "true" {
		return nil
	}
	liveLinks.mu.Lock()
	links := make([]*link, 0, len(liveLinks.links))
	decides := false
	for l := range liveLinks.links {
		links = append(links, l)
		decides = decides || l.cfg.sessions != nil
	}
	liveLinks.mu.Unlock()
	// A thread turn parks only where a decision can reach it: Studio
	// decides an app turn through a live link's Sessions resolver, by
	// public id. With no such link, or a session without a public id,
	// nobody but the app could decide the park, and under ADR 0021 §5
	// every later Send would queue behind it — a breakpoint must not
	// wedge a session the app has no approval UI for (stop()'s
	// reasoning: a runtime that cannot decide does not park).
	if md[sessionKey] != "" && (!decides || md[publicIDKey] == "") {
		return nil
	}
	var set map[string]bool
	for _, l := range links {
		for _, t := range l.breakpointSet() {
			if set == nil {
				set = map[string]bool{}
			}
			set[t] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// breakpointSourcePC identifies breakpointSource among an agent's
// ParkSources: a top-level function's code pointer is the same for
// every func value of it.
var breakpointSourcePC = reflect.ValueOf(breakpointSource).Pointer()

// carriesBreakpoints reports whether a was built with Breakpoints().
func carriesBreakpoints(a *core.Agent) bool {
	if a == nil {
		return false
	}
	for _, fn := range a.ParkSources() {
		if reflect.ValueOf(fn).Pointer() == breakpointSourcePC {
			return true
		}
	}
	return false
}
