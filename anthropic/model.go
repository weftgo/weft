package anthropic

import (
	"os"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/weftgo/weft"
)

// Option configures the adapter at construction, the same functional
// style as the core. The zero configuration reads $ANTHROPIC_API_KEY
// and does not retry.
type Option interface{ apply(*config) }

type config struct {
	client      *anthropic.Client
	baseURL     string
	apiKey      string
	maxTokens   int
	temperature float64
	tempSet     bool
	idle        time.Duration
	idleSet     bool
	maxRetries  int
	thinking    bool
}

type optionFunc func(*config)

func (f optionFunc) apply(c *config) { f(c) }

// BaseURL points the adapter at a custom endpoint (a gateway, a
// proxy). Also read from $ANTHROPIC_BASE_URL when the option is not
// given.
func BaseURL(u string) Option { return optionFunc(func(c *config) { c.baseURL = u }) }

// APIKey sets the API key. Default: $ANTHROPIC_API_KEY.
func APIKey(k string) Option { return optionFunc(func(c *config) { c.apiKey = k }) }

// Client uses an already-configured SDK client (Vertex AI and Bedrock
// clients compose here); it overrides BaseURL, APIKey, and MaxRetries.
func Client(c *anthropic.Client) Option {
	return optionFunc(func(cfg *config) { cfg.client = c })
}

// MaxTokens sets the per-step output-token limit. Anthropic requires
// the field; the adapter defaults it to 4096 when unset.
func MaxTokens(n int) Option { return optionFunc(func(c *config) { c.maxTokens = n }) }

// Temperature sets the sampling temperature; it is not sent unless the
// option is given.
func Temperature(t float64) Option {
	return optionFunc(func(c *config) { c.temperature = t; c.tempSet = true })
}

// IdleTimeout is the maximum gap between two stream events before the
// call fails wrapping weft.ErrStreamIdle (default 60s; zero disables
// it). The ctx deadline stays the hard limit on the whole call — a
// slow but actively streaming response is never killed.
func IdleTimeout(d time.Duration) Option {
	return optionFunc(func(c *config) { c.idle = d; c.idleSet = true })
}

// MaxRetries forwards to the SDK's transport retry configuration
// (429/5xx/connection errors only). The weft loop never retries a model
// call; logic retries are model-seam middleware (TODO §4.1).
func MaxRetries(n int) Option { return optionFunc(func(c *config) { c.maxRetries = n }) }

// Thinking enables adaptive thinking: the request carries
// thinking:{"type":"adaptive"} and the stream surfaces thinking blocks
// (with their signatures) as reasoning. Without the option nothing is
// sent — the vendor default for the model applies.
func Thinking(on bool) Option { return optionFunc(func(c *config) { c.thinking = on }) }

const (
	defaultIdleTimeout = 60 * time.Second
	defaultMaxTokens   = 4096
	provider           = "anthropic"
)

// Model returns a weft.Model backed by the Anthropic Messages API. A
// Model is immutable and safe for concurrent runs; tool definitions
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
		thinking:    cfg.thinking,
		idle:        defaultIdleTimeout,
		tools:       sync.Map{},
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
	} else if u := os.Getenv("ANTHROPIC_BASE_URL"); u != "" {
		sdkOpts = append(sdkOpts, option.WithBaseURL(u))
	}
	if cfg.apiKey != "" {
		sdkOpts = append(sdkOpts, option.WithAPIKey(cfg.apiKey))
	}
	if cfg.maxRetries > 0 {
		sdkOpts = append(sdkOpts, option.WithMaxRetries(cfg.maxRetries))
	}
	m.client = anthropic.NewClient(sdkOpts...)
	return m
}

type model struct {
	client      anthropic.Client
	name        string
	maxTokens   int
	temperature float64
	tempSet     bool
	thinking    bool
	idle        time.Duration
	tools       sync.Map // *weft.ToolDef → anthropic.ToolUnionParam
}

// Info identifies the model for RunStart and the manifest.
func (m *model) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: provider, Name: m.name}
}
