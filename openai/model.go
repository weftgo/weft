package openai

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/internal/adapterkit"
)

// Option configures the adapter at construction, the same functional
// style as the core. The zero configuration reads $OPENAI_API_KEY (and
// $OPENAI_BASE_URL); the SDK's transport default applies (2 retries on
// 429/5xx/connection errors).
type Option interface{ apply(*config) }

type config struct {
	client       *openai.Client
	baseURL      string
	apiKey       string
	maxTokens    int
	temperature  float64
	tempSet      bool
	topP         float64
	topPSet      bool
	stop         []string
	seed         int64
	seedSet      bool
	extraBody    map[string]any
	extraHeaders http.Header
	idle         time.Duration
	idleSet      bool
	maxRetries   int
	dialect      ThinkingDialect
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

// Client uses an already-configured SDK client (Azure endpoints,
// custom transports, test doubles); it overrides BaseURL, APIKey, and
// MaxRetries. The WEFT_MODEL_REQUESTS kill switch guards egress from
// clients the adapter builds from credentials; an injected client's
// destinations are the caller's responsibility — which is why it
// stays reachable under deny (ADR 0013's kill-switch clause).
func Client(c *openai.Client) Option {
	return optionFunc(func(cfg *config) { cfg.client = c })
}

// MaxTokens caps a step's output tokens (max_completion_tokens). Zero
// keeps the provider default.
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

// Stop sets stop sequences the API stops on; not sent unless the
// option is given. A per-request weft.RequestParams.Stop overrides it
// for one call.
func Stop(seqs ...string) Option {
	return optionFunc(func(c *config) { c.stop = seqs })
}

// Seed sets the sampling seed for deterministic-ish runs — a hint the
// provider treats best-effort, not a contract. Not sent unless the
// option is given; a per-request weft.RequestParams.Seed overrides it
// for one call.
func Seed(s int64) Option {
	return optionFunc(func(c *config) { c.seed = s; c.seedSet = true })
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

// ExtraBody adds fields to every request's JSON body — the generic
// valve for vendor knobs weft has no option for (the public version of
// the gateway thinking injection). Deep-merged into the body weft
// built: nested maps merge recursively, every other value replaces,
// and **your key wins on conflict** — the escape hatch is you taking
// responsibility for bytes weft did not choose, and the default-bytes
// tests do not cover what it sends. Construction-time only, and a snapshot: the values are
// deep-copied when the option applies, so mutating the map you passed
// afterwards never reaches the Model (safe for concurrent runs). It
// applies to the requests the adapter makes, including through an
// injected Client(c).
func ExtraBody(fields map[string]any) Option {
	return optionFunc(func(c *config) {
		if c.extraBody == nil {
			c.extraBody = map[string]any{}
		}
		for k, v := range fields {
			c.extraBody[k] = adapterkit.CloneJSON(v)
		}
	})
}

// ExtraHeaders adds HTTP headers to every request, verbatim. A header
// the SDK itself sets (Authorization, Content-Type) is yours not to
// clobber — the option does not check. Construction-time only, and a
// snapshot: the slices are copied when the option applies, so mutating
// the header values you passed afterwards never reaches the Model.
func ExtraHeaders(h http.Header) Option {
	return optionFunc(func(c *config) {
		if c.extraHeaders == nil {
			c.extraHeaders = http.Header{}
		}
		for k, vs := range h {
			c.extraHeaders[k] = slices.Clone(vs)
		}
	})
}

const (
	defaultIdleTimeout = 60 * time.Second
	// provider is fixed even for compatible servers; see BaseURL.
	provider = "openai"
)

// Model returns a weft.Model backed by the OpenAI Chat Completions API
// (or any compatible server, via BaseURL). A Model is immutable and
// safe for concurrent runs.
func Model(name string, opts ...Option) weft.Model {
	var cfg config
	for _, o := range opts {
		if o != nil {
			o.apply(&cfg)
		}
	}
	m := &model{
		name:         name,
		maxTokens:    cfg.maxTokens,
		temperature:  cfg.temperature,
		tempSet:      cfg.tempSet,
		topP:         cfg.topP,
		topPSet:      cfg.topPSet,
		stop:         cfg.stop,
		seed:         cfg.seed,
		seedSet:      cfg.seedSet,
		extraBody:    cfg.extraBody,
		extraHeaders: cfg.extraHeaders,
		idle:         defaultIdleTimeout,
		dialect:      resolveDialect(cfg.dialect, cfg.baseURL),
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
	client       openai.Client
	injected     bool // client came from Client(c): a test double, exempt from the kill switch
	name         string
	maxTokens    int
	temperature  float64
	tempSet      bool
	topP         float64
	topPSet      bool
	stop         []string
	seed         int64
	seedSet      bool
	extraBody    map[string]any
	extraHeaders http.Header
	idle         time.Duration
	dialect      ThinkingDialect
}

// requestOptions builds the per-request options the escape hatch adds:
// a middleware deep-merging ExtraBody's fields into the JSON body
// (adapterkit.MergeBody, caller wins) and one header option per
// ExtraHeaders entry. They ride after the adapter's own body
// injection, so the caller's merge sees weft's bytes and overrides
// them (ADR 0013's 2026-09-22 amendment).
func (m *model) requestOptions() []option.RequestOption {
	var opts []option.RequestOption
	if len(m.extraBody) > 0 {
		extra := m.extraBody
		opts = append(opts, option.WithMiddleware(func(r *http.Request, next option.MiddlewareNext) (*http.Response, error) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			merged, err := adapterkit.MergeBody(body, extra)
			if err != nil {
				return nil, fmt.Errorf("openai adapter: ExtraBody: %w", err)
			}
			r.Body = io.NopCloser(bytes.NewReader(merged))
			r.ContentLength = int64(len(merged))
			return next(r)
		}))
	}
	for k, vs := range m.extraHeaders {
		// WithHeader has Set semantics — looping it over one key's
		// values would keep only the last. The first value sets, the
		// rest add, so a multi-valued header (X-Multi: a, b) reaches
		// the wire whole, verbatim as documented.
		for i, v := range vs {
			if i == 0 {
				opts = append(opts, option.WithHeader(k, v))
			} else {
				opts = append(opts, option.WithHeaderAdd(k, v))
			}
		}
	}
	return opts
}

// Info identifies the model for RunStart and the manifest. The provider
// is "openai" even when BaseURL points elsewhere.
func (m *model) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: provider, Name: m.name}
}
