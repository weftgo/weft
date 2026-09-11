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

// Client uses an already-configured SDK client (Vertex AI projects and
// regions compose here); it overrides BaseURL, APIKey, and MaxRetries.
func Client(c *genai.Client) Option {
	return optionFunc(func(cfg *config) { cfg.client = c })
}

// MaxTokens caps a step's output tokens (maxOutputTokens). Zero keeps
// the provider default.
func MaxTokens(n int) Option { return optionFunc(func(c *config) { c.maxTokens = n }) }

// Temperature sets the sampling temperature; it is not sent unless the
// option is given.
func Temperature(t float64) Option {
	return optionFunc(func(c *config) { c.temperature = t; c.tempSet = true })
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
// A Model is immutable and safe for concurrent runs; tool definitions
// are converted once per *ToolDef and cached by pointer.
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
		idle:        defaultIdleTimeout,
		tools:       sync.Map{},
	}
	if cfg.idleSet {
		m.idle = cfg.idle
	}
	if cfg.client != nil {
		m.client = cfg.client
		m.ready = true
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
	client    *genai.Client
	clientErr error
	ready     bool
	once      sync.Once

	name        string
	baseURL     string
	apiKey      string
	maxRetries  int
	maxTokens   int
	temperature float64
	tempSet     bool
	idle        time.Duration
	tools       sync.Map // *weft.ToolDef → *genai.Tool
}

// initClient builds the SDK client on the first Stream call.
func (m *model) initClient(ctx context.Context) error {
	m.once.Do(func() {
		if m.ready {
			return
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
		m.client, m.clientErr = genai.NewClient(ctx, cc)
		m.ready = m.clientErr == nil
	})
	return m.clientErr
}

// Info identifies the model for RunStart and the manifest.
func (m *model) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: provider, Name: m.name}
}
