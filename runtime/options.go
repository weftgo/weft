package runtime

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"os"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/internal/discovery"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/version"
)

// Option configures Install.
type Option func(*config)

// config is everything Install was told. It is built once and read by
// the link goroutine; nothing mutates it afterwards.
type config struct {
	studioURL   string
	studioToken string
	local       *studio.Server // setup A: the embedded Studio, in-process
	agents      []*core.Agent
	models      map[string]core.Model
	resolve     func(ctx context.Context, name string) (core.Model, error)
	budget      Budget
	allow       map[string]bool
	threads     thread.Storage
	enabled     *bool
}

// Studio dials the Studio at url with token (setups B and C: the local
// binary or the hosted one). Empty token is the dev mode of setup B.
// Without a Studio or Local option, Install falls back to the pipeline's
// own Studio destination — otel.StudioEndpoint() — then, when that is
// empty, to the discovery file a running `weft studio` / `weft dev`
// wrote (unless WEFT_STUDIO_URL is set or WEFT_DISCOVERY=off; a stale
// file is never trusted), and opens nothing when there is none.
func Studio(url, token string) Option {
	return func(c *config) { c.studioURL, c.studioToken = url, token }
}

// Local takes the embedded Studio server (setup A): the link talks to
// srv's handler in-process, no socket. srv is the studio.New(...)
// server the app mounts; nil is ignored. In-process is not exempt from
// srv's own token gate: a server built with studio.Token needs the
// token here as well — add Studio("", token).
func Local(srv *studio.Server) Option {
	return func(c *config) { c.local = srv }
}

// Agents registers the agents this runtime exposes. Only registered
// agents are playable (WEFT-PLAYGROUND.md §5.2). An agent without a
// name (core.Name) cannot register — its manifest would be nameless —
// and is skipped with a WARN.
func Agents(agents ...*core.Agent) Option {
	return func(c *config) { c.agents = append(c.agents, agents...) }
}

// Models declares the model alternates a command may switch to, by
// display name — the allow-list the playground's model picker reads
// and the resolver that turns a command's model string back into a
// core.Model (§5.2). A name missing here is refused: the playground
// cannot add a model, only choose among the ones the code registered.
func Models(models map[string]core.Model) Option {
	return func(c *config) {
		if c.models == nil {
			c.models = make(map[string]core.Model, len(models))
		}
		for name, m := range models {
			c.models[name] = m
		}
	}
}

// ModelResolver lets a command name a model the runtime did not list
// in Models — "try this on claude-haiku-4-5" without pre-registering
// every model. A command's model override that is neither the agent's
// own model name nor a Models name is handed to resolve while the
// command is validated, before its ack: a model back runs the command
// (the run's RunStart.Model and weft.override.model name it); an error
// rejects the command with "model <name>: <err.Error()>".
//
// resolve is the app's code, so the playground still only narrows:
// Studio proposes a name, the app decides whether it exists — build
// the client from the app's own credentials, refuse names it does not
// support. The error text is shown to whoever ran the experiment, so
// it must carry no key, URL or secret; the app controls the message.
// resolve may be called concurrently and once per command; cache
// clients if building one is costly. Each call is bounded: its ctx is
// canceled after 10 s and the command is rejected with "model <name>:
// resolver timed out" (the command runs before its ack, and an ack
// Studio waits on too long is marked lost); a resolver that ignores
// ctx is abandoned at the bound and its late result dropped — honour
// ctx, or the abandoned call keeps running. Registration reports the flag
// (resolver: true), and Studio accepts an unlisted name only then. A
// nil resolve is ignored.
func ModelResolver(resolve func(ctx context.Context, name string) (core.Model, error)) Option {
	return func(c *config) {
		if resolve != nil {
			c.resolve = resolve
		}
	}
}

// Budget caps one experiment, counted per experiment_id from the usage
// of the commands this runtime ran (§6 rule 6). Zero fields are no
// caps. A breach rejects the *next* command of that experiment
// (budget_exceeded) — never a run in flight, never the app's own runs.
type Budget struct {
	MaxTokensPerExperiment int64 // input + output tokens
	MaxRunsPerExperiment   int64
}

// Limits sets this runtime's per-experiment caps. Without it, a
// runtime accepts unlimited playground runs within the agent's own
// budgets (MaxSteps, UsageLimit).
func Limits(b Budget) Option {
	return func(c *config) { c.budget = b }
}

// AllowSideEffects opts tools in to really running in playground runs
// whose command asks for side_effects "allow" (WEFT-PLAYGROUND §5.1,
// §6 rule 3): under "allow" a tool named here is never parked and never
// substituted — its handler executes whenever the experiment's model
// calls it. In "substitute" (the default) and "park" an opted-in tool
// is a side effect like any other: a call matching one the source run
// recorded is answered with the recorded result, any other call parks
// at the approval boundary ("park" answers nothing from the record).
// A tool marked core.Replay(core.ReplaySafe) is not a side effect and
// runs in every mode, opted in or not. A command that asks for "allow"
// is refused unless every tool it leaves on is named here or vouched
// ReplaySafe — "allow" runs this list for real, it does not widen it.
//
// Name a tool here only when re-running it is harmless. Names match
// wherever the run reaches: a tool a core.ToolSource supplies under
// that name, and a tool of that name in a core.Subagent child (the
// child is under the same rule — its other tools park).
func AllowSideEffects(tools ...string) Option {
	return func(c *config) {
		if c.allow == nil {
			c.allow = make(map[string]bool, len(tools))
		}
		for _, t := range tools {
			c.allow[t] = true
		}
	}
}

// Threads sets the thread.Storage transcript reads resolve through
// first (WEFT-PLAYGROUND.md §10.3): readers never lock, so an open
// session is readable. Without it the runtime is ephemeral-only and
// resolves transcripts from its local obsdb or from Studio. nil is
// ignored.
func Threads(store thread.Storage) Option {
	return func(c *config) { c.threads = store }
}

// Enabled opens (or forbids) the link explicitly. The default — no
// Enabled option — is on only when WEFT_ENV=dev: production binaries
// expose the runtime link only by explicit opt-in (§6 rule 1).
func Enabled(on bool) Option {
	return func(c *config) { c.enabled = &on }
}

// Install registers the agents with a Studio and starts the runtime
// link: it dials out (never listens), so it works behind NAT. It never
// panics and never fails the program — a runtime that cannot be built
// (nothing enabled, no endpoint, no named agents) logs a WARN and
// opens nothing. The returned function stops the link — the command
// stream ends, the runs the link started are canceled, and it waits
// (bounded) for both; call it on exit, like otel.Install's. Calling it
// again is harmless.
//
// The link registers on connect and after every reconnect, receives
// commands over an SSE stream (resuming with Last-Event-ID), acks every
// command before executing it (at-most-once: a repeated command id is
// ignored), executes it as a run of the named agent, and reports the
// run's end with a finished ack. The run's content flows back through
// the normal OTel pipeline, not through the link.
func Install(opts ...Option) (shutdown func()) {
	c := &config{}
	for _, o := range opts {
		if o != nil {
			o(c)
		}
	}
	if c.enabled != nil && !*c.enabled {
		return func() {}
	}
	if c.enabled == nil && os.Getenv("WEFT_ENV") != "dev" {
		return func() {}
	}
	if len(c.agents) == 0 {
		slog.Warn("weft/runtime: Install without agents: no link opened",
			"hint", "runtime.Agents(...) names the agents a runtime exposes")
		return func() {}
	}
	url, token := c.studioURL, c.studioToken
	if c.local == nil && url == "" {
		// The default endpoint: the pipeline's own Studio destination
		// (otel.StudioEndpoint, S2.1). weft/otel knows nothing about
		// this package; this is the one place the two meet.
		url, token = otel.StudioEndpoint()
	}
	if c.local == nil && url == "" {
		// The discovery rung (plan B3): the file `weft studio` / `weft
		// dev` writes names the running Studio — never read when
		// WEFT_STUDIO_URL is set or WEFT_DISCOVERY=off, never trusted
		// when stale. One INFO line says which Studio this joined.
		if info, path, ok := discovery.Lookup(os.Getenv, slog.Default()); ok {
			url, token = info.URL, info.Token
			slog.Info("weft/runtime: joined the running Studio", "url", info.URL, "pid", info.PID, "file", path,
				"hint", "WEFT_STUDIO_URL or runtime.Studio(url, token) names another; WEFT_DISCOVERY=off ignores the file")
		}
	}
	if c.local == nil && url == "" {
		slog.Warn("weft/runtime: no Studio to dial: no link opened",
			"hint", "runtime.Studio(url, token), runtime.Local(srv), or a Studio destination in weft/otel")
		return func() {}
	}
	if c.local == nil && token != "" && cleartext(url) {
		slog.Warn("weft/runtime: the Studio token travels unencrypted over http to a non-loopback host",
			"hint", "use an https Studio URL")
	}
	reg := newRegistry(c)
	l := newLink(c, reg, url, token)
	if err := l.start(); err != nil {
		slog.Warn("weft/runtime: link failed to start", "err", err)
		return func() {}
	}
	return l.stop
}

// cleartext reports whether rawURL is plain http to a host that is not
// this machine — where a bearer token would cross a network readable.
func cleartext(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return false
	}
	return true
}

// weftVersion is the framework module's tag, [version.Version] — the
// constant, not version.Runtime, because the core stamps weft.version
// on the same run's spans and records from its own literal, and one run
// must carry one value. TestWeftVersionMatchesRoot pins the two.
func weftVersion() string { return version.Version }
