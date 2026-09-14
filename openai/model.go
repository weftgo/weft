package openai

import (
	"os"
	"sync"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/weftgo/weft"
)

// Option configures the adapter at construction, the same functional
// style as the core. The zero configuration reads $OPENAI_API_KEY (and
// $OPENAI_BASE_URL); the SDK's transport default applies (2 retries on
// 429/5xx/connection errors).
type Option interface{ apply(*config) }

type config struct {
	client      *openai.Client
	baseURL     string
	apiKey      string
	maxTokens   int
	temperature float64
	tempSet     bool
	idle        time.Duration
	idleSet     bool
	maxRetries  int
	dialect     ThinkingDialect
}

type optionFunc func(*config)

func (f optionFunc) apply(c *config) { f(c) }

// BaseURL points the adapter at an OpenAI-compatible server — gateways,
// local models. Also read from $OPENAI_BASE_URL when the option is not
// given. The provider name stays "openai": a base URL host is not a
// provider identity and would leak into telemetry cardinality.
func BaseURL(u string) Option { return optionFunc(func(c *config) { c.baseURL = u }) }

// APIKey sets the API key. Default: $OPENAI_API_KEY.
func APIKey(k string) Option { return optionFunc(func(c *config) { c.apiKey = k }) }

// Client uses an already-configured SDK client (Azure, custom
// transports); it overrides BaseURL, APIKey, and MaxRetries.
func Client(c *openai.Client) Option {
	return optionFunc(func(cfg *config) { cfg.client = c })
}

// MaxTokens caps a step's output tokens (max_completion_tokens). Zero
// keeps the provider default.
func MaxTokens(n int) Option { return optionFunc(func(c *config) { c.maxTokens = n }) }

// Temperature sets the sampling temperature; it is not sent unless the
// option is given.
func Temperature(t float64) Option {
	return optionFunc(func(c *config) { c.temperature = t; c.tempSet = true })
}

// IdleTimeout is the maximum gap between two stream chunks before the
// call fails wrapping weft.ErrStreamIdle (default 60s; zero disables
// it). The ctx deadline stays the hard limit on the whole call — a slow
// but actively streaming response is never killed.
func IdleTimeout(d time.Duration) Option {
	return optionFunc(func(c *config) { c.idle = d; c.idleSet = true })
}

// MaxRetries forwards to the SDK's transport retry configuration
// (429/5xx/connection errors only). Only n > 0 is forwarded: the SDK's
// own default (2) applies otherwise, and 0 cannot disable it — keep a
// zero-retry client via Client(c) if you need one. The weft loop never
// retries a model call; logic retries are model-seam middleware
// (TODO §4.1).
func MaxRetries(n int) Option { return optionFunc(func(c *config) { c.maxRetries = n }) }

const (
	defaultIdleTimeout = 60 * time.Second
	// provider is fixed even for compatible servers; see BaseURL.
	provider = "openai"
)

// Model returns a weft.Model backed by the OpenAI Chat Completions API
// (or any compatible server, via BaseURL). A Model is immutable and
// safe for concurrent runs; tool definitions are converted once per
// *ToolDef and cached by pointer.
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
		dialect:     resolveDialect(cfg.dialect, cfg.baseURL),
	}
	if cfg.idleSet {
		m.idle = cfg.idle
	}
	if cfg.client != nil {
		m.client = *cfg.client
		return m
	}
	var sdkOpts []option.RequestOption
	if cfg.baseURL != "" {
		sdkOpts = append(sdkOpts, option.WithBaseURL(cfg.baseURL))
	} else if u := os.Getenv("OPENAI_BASE_URL"); u != "" {
		sdkOpts = append(sdkOpts, option.WithBaseURL(u))
	}
	if cfg.apiKey != "" {
		sdkOpts = append(sdkOpts, option.WithAPIKey(cfg.apiKey))
	}
	if cfg.maxRetries > 0 {
		sdkOpts = append(sdkOpts, option.WithMaxRetries(cfg.maxRetries))
	}
	m.client = openai.NewClient(sdkOpts...)
	return m
}

type model struct {
	client      openai.Client
	name        string
	maxTokens   int
	temperature float64
	tempSet     bool
	idle        time.Duration
	dialect     ThinkingDialect
	tools       sync.Map // *weft.ToolDef → openai.ChatCompletionToolParam
}

// Info identifies the model for RunStart and the manifest. The provider
// is "openai" even when BaseURL points elsewhere.
func (m *model) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: provider, Name: m.name}
}
