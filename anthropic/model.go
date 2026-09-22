package anthropic

import (
	"os"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/weftgo/weft"
)

// Option configures the adapter at construction, the same functional
// style as the core. The zero configuration reads $ANTHROPIC_API_KEY;
// the SDK's transport default applies (2 retries on
// 429/5xx/connection errors).
type Option interface{ apply(*config) }

type config struct {
	client      *anthropic.Client
	baseURL     string
	apiKey      string
	maxTokens   int
	temperature float64
	tempSet     bool
	topP        float64
	topPSet     bool
	stop        []string
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
// clients, test doubles); it overrides BaseURL, APIKey, and MaxRetries.
// The WEFT_MODEL_REQUESTS kill switch guards egress from clients the
// adapter builds from credentials; an injected client's destinations
// are the caller's responsibility — which is why it stays reachable
// under deny (ADR 0013's kill-switch clause).
func Client(c *anthropic.Client) Option {
	return optionFunc(func(cfg *config) { cfg.client = c })
}

// MaxTokens sets the per-step output-token limit. Anthropic requires
// the field; the adapter defaults it to 4096 when unset.
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

// Stop sets stop sequences (stop_sequences); not sent unless the
// option is given. A per-request weft.RequestParams.Stop overrides it
// for one call. The Messages API has no seed — a RequestParams.Seed is
// dropped (see doc.go), because seed is a determinism hint everywhere,
// not a contract.
func Stop(seqs ...string) Option {
	return optionFunc(func(c *config) { c.stop = seqs })
}

// IdleTimeout is the maximum gap between two stream events before the
// call fails wrapping weft.ErrStreamIdle (default 60s; zero disables
// it). The ctx deadline stays the hard limit on the whole call — a
// slow but actively streaming response is never killed.
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
		topP:        cfg.topP,
		topPSet:     cfg.topPSet,
		stop:        cfg.stop,
		thinking:    cfg.thinking,
		idle:        defaultIdleTimeout,
	}
	if cfg.idleSet {
		m.idle = cfg.idle
	}
	if cfg.client != nil {
		m.client = *cfg.client
		m.injected = true
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
	injected    bool // client came from Client(c): a test double, exempt from the kill switch
	name        string
	maxTokens   int
	temperature float64
	tempSet     bool
	topP        float64
	topPSet     bool
	stop        []string
	thinking    bool
	idle        time.Duration
}

// Info identifies the model for RunStart and the manifest.
func (m *model) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: provider, Name: m.name}
}
