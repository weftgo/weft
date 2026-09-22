package google

import (
	"context"
	"sync"
	"time"

	"github.com/weftgo/weft"
	"google.golang.org/genai"
)

// Option configures the adapter at construction, the same functional
// style as the core. The zero configuration discovers credentials the
// SDK's way ($GOOGLE_API_KEY / $GEMINI_API_KEY, then application
// default) and does not set a base URL.
type Option interface{ apply(*config) }

type config struct {
	client      *genai.Client
	baseURL     string
	apiKey      string
	maxTokens   int
	temperature float64
	tempSet     bool
	topP        float64
	topPSet     bool
	stop        []string
	seed        int64
	seedSet     bool
	idle        time.Duration
	idleSet     bool
	maxRetries  int
}

type optionFunc func(*config)

func (f optionFunc) apply(c *config) { f(c) }

// BaseURL points the adapter at a custom endpoint (a gateway, a
// proxy). When the option is not given the SDK applies its own
// $GOOGLE_GEMINI_BASE_URL.
func BaseURL(u string) Option { return optionFunc(func(c *config) { c.baseURL = u }) }

// APIKey sets the API key. Default: the SDK's own discovery
// ($GOOGLE_API_KEY / $GEMINI_API_KEY, then application default
// credentials).
func APIKey(k string) Option { return optionFunc(func(c *config) { c.apiKey = k }) }

// Client uses an already-configured SDK client (Vertex AI projects
// and regions, test doubles); it overrides BaseURL, APIKey, and
// MaxRetries. The WEFT_MODEL_REQUESTS kill switch guards egress from
// clients the adapter builds from credentials; an injected client's
// destinations are the caller's responsibility — which is why it
// stays reachable under deny (ADR 0013's kill-switch clause).
func Client(c *genai.Client) Option {
	return optionFunc(func(cfg *config) { cfg.client = c })
}

// MaxTokens caps a step's output tokens (maxOutputTokens). Zero keeps
// the provider default. The API's limit is an int32: a value above
// math.MaxInt32 fails the call wrapping weft.ErrUnsupported rather
// than wrapping around on the wire. Options carry no error channel, so
// the check lands at convert time — the first place that can refuse —
// not at construction.
func MaxTokens(n int) Option { return optionFunc(func(c *config) { c.maxTokens = n }) }

// Temperature sets the sampling temperature; it is not sent unless the
// option is given. A per-request weft.RequestParams.Temperature
// overrides it for one call.
func Temperature(t float64) Option {
	return optionFunc(func(c *config) { c.temperature = t; c.tempSet = true })
}

// TopP sets nucleus sampling; it is not sent unless the option is
// given. A per-request weft.RequestParams.TopP overrides it for one
// call.
func TopP(p float64) Option {
	return optionFunc(func(c *config) { c.topP = p; c.topPSet = true })
}

// Stop sets stop sequences; not sent unless the option is given. A
// per-request weft.RequestParams.Stop overrides it for one call.
func Stop(seqs ...string) Option {
	return optionFunc(func(c *config) { c.stop = seqs })
}

// Seed sets the sampling seed — a best-effort determinism hint, not a
// contract. Not sent unless the option is given; a per-request
// weft.RequestParams.Seed overrides it for one call. The wire field is
// an int32: a value outside that range fails the call wrapping
// weft.ErrUnsupported at convert time (options carry no error channel
// — the MaxTokens rule).
func Seed(s int64) Option {
	return optionFunc(func(c *config) { c.seed = s; c.seedSet = true })
}

// IdleTimeout is the maximum gap between two stream chunks before the
// call fails wrapping weft.ErrStreamIdle (default 60s; zero disables
// it). The ctx deadline stays the hard limit on the whole call — a
// slow but actively streaming response is never killed.
func IdleTimeout(d time.Duration) Option {
	return optionFunc(func(c *config) { c.idle = d; c.idleSet = true })
}

// MaxRetries forwards to the SDK's transport retry configuration
// (429/5xx/connection errors only; the SDK retries nothing unless
// asked). The weft loop never retries a model call; logic retries are
// model-seam middleware (TODO §4.1).
func MaxRetries(n int) Option { return optionFunc(func(c *config) { c.maxRetries = n }) }

const (
	defaultIdleTimeout = 60 * time.Second
	provider           = "google"
)

// Model returns a weft.Model backed by the Gemini API. The SDK client
// is created lazily on the first run (its constructor wants a context
// for credential discovery), so constructing a Model performs no I/O.
// A Model is immutable and safe for concurrent runs.
func Model(name string, opts ...Option) weft.Model {
	var cfg config
	for _, o := range opts {
		if o != nil {
			o.apply(&cfg)
		}
	}
	m := &model{
		name:        name,
		maxTokens:   cfg.maxTokens,
		temperature: cfg.temperature,
		tempSet:     cfg.tempSet,
		topP:        cfg.topP,
		topPSet:     cfg.topPSet,
		stop:        cfg.stop,
		seed:        cfg.seed,
		seedSet:     cfg.seedSet,
		idle:        defaultIdleTimeout,
	}
	if cfg.idleSet {
		m.idle = cfg.idle
	}
	if cfg.client != nil {
		m.client = cfg.client
		m.ready = true
		m.injected = true
	} else {
		m.baseURL = cfg.baseURL
		m.apiKey = cfg.apiKey
		m.maxRetries = cfg.maxRetries
	}
	return m
}

type model struct {
	// client is created on first use: genai.NewClient wants a context
	// for credential discovery, and Model itself performs no I/O.
	// initMu guards the lazy init; a failed attempt is not cached —
	// credential discovery can be transient (metadata-server hiccup,
	// an already-canceled first ctx), and caching it would brick the
	// Model value forever.
	client *genai.Client
	ready  bool
	// injected: the client came from Client(c) — a test double by
	// construction, exempt from the kill switch — so no lazy init.
	injected bool
	initMu   sync.Mutex

	name        string
	baseURL     string
	apiKey      string
	maxRetries  int
	maxTokens   int
	temperature float64
	tempSet     bool
	topP        float64
	topPSet     bool
	stop        []string
	seed        int64
	seedSet     bool
	idle        time.Duration
}

// initClient builds the SDK client on the first Stream call.
func (m *model) initClient(ctx context.Context) error {
	m.initMu.Lock()
	defer m.initMu.Unlock()
	if m.ready {
		return nil
	}
	cc := &genai.ClientConfig{APIKey: m.apiKey}
	if m.baseURL != "" {
		cc.HTTPOptions.BaseURL = m.baseURL
	}
	if m.maxRetries > 0 {
		// Attempts counts the original request.
		attempts := int32(m.maxRetries + 1)
		cc.HTTPOptions.RetryOptions = &genai.HTTPRetryOptions{Attempts: &attempts}
	}
	client, err := genai.NewClient(ctx, cc)
	if err != nil {
		return err // not cached: the next call retries
	}
	m.client, m.ready = client, true
	return nil
}

// Info identifies the model for RunStart and the manifest.
func (m *model) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: provider, Name: m.name}
}
