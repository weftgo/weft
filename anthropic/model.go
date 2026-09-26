package anthropic

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/internal/adapterkit"
)

// Option configures the adapter at construction, the same functional
// style as the core. The zero configuration reads $ANTHROPIC_API_KEY;
// the SDK's transport default applies (2 retries on
// 429/5xx/connection errors).
type Option interface{ apply(*config) }

type config struct {
	client       *anthropic.Client
	baseURL      string
	apiKey       string
	maxTokens    int
	temperature  float64
	tempSet      bool
	topP         float64
	topPSet      bool
	stop         []string
	extraBody    map[string]any
	extraHeaders http.Header
	idle         time.Duration
	idleSet      bool
	maxRetries   int
	thinking     bool
	promptCache  bool
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

// PromptCache marks the request's stable prefix edges as cacheable:
// cache_control:{"type":"ephemeral"} on the system text block, the
// final tool definition, and the final content block of the final
// message — three of Anthropic's four breakpoint budget, placed where
// the transcript grows (Crush's placement; the fourth stays unspent
// for a compaction summary block). Opt-in: without the option no
// cache_control appears anywhere, and the request bytes are exactly
// v0.2.0's.
//
// Economics: cache writes bill 1.25× and cache reads 0.1× the base
// input-token price, so a long transcript whose prefix repeats across
// steps saves from the second step on — watch Usage's
// CachedInputTokens and CacheWriteTokens to see it measured. Prefix
// discipline is the caller's: a PrepareStep function that trims
// messages invalidates the trailing breakpoint on purpose (the option
// composes with deliberate trimming, it does not forbid it), and
// weft.ToolChoiceNone is the way to stop tool calls without dropping
// the tool definitions — and the cache prefix they anchor — from the
// request. No TTL or position options in v0.3.0: one good default,
// revisit when a consumer asks.
func PromptCache() Option { return optionFunc(func(c *config) { c.promptCache = true }) }

// ExtraBody adds fields to every request's JSON body — the generic
// valve for vendor knobs weft has no option for. Deep-merged into the
// body weft built: nested maps merge recursively, every other value
// replaces, and **your key wins on conflict** — the escape hatch is
// you taking responsibility for bytes weft did not choose, and the
// default-bytes tests do not cover what it sends. Construction-time
// only, and a snapshot: the values are deep-copied when the option
// applies, so mutating the map you passed afterwards never reaches the
// Model (safe for concurrent runs). It applies to the requests the
// adapter makes, including through an injected Client(c).
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
	defaultMaxTokens   = 4096
	provider           = "anthropic"
)

// Model returns a weft.Model backed by the Anthropic Messages API. A
// Model is immutable and safe for concurrent runs; tool definitions
// are converted per request (ADR 0013).
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
		extraBody:    cfg.extraBody,
		extraHeaders: cfg.extraHeaders,
		thinking:     cfg.thinking,
		promptCache:  cfg.promptCache,
		idle:         defaultIdleTimeout,
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
	client       anthropic.Client
	injected     bool // client came from Client(c): a test double, exempt from the kill switch
	name         string
	maxTokens    int
	temperature  float64
	tempSet      bool
	topP         float64
	topPSet      bool
	stop         []string
	extraBody    map[string]any
	extraHeaders http.Header
	thinking     bool
	promptCache  bool
	idle         time.Duration
}

// requestOptions builds the per-request options the escape hatch adds:
// a middleware deep-merging ExtraBody's fields into the JSON body
// (adapterkit.MergeBody, caller wins) and one header option per
// ExtraHeaders entry (ADR 0013's 2026-09-22 amendment).
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
				return nil, fmt.Errorf("anthropic adapter: ExtraBody: %w", err)
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

// Info identifies the model for RunStart and the manifest.
func (m *model) Info() weft.ModelInfo {
	return weft.ModelInfo{Provider: provider, Name: m.name}
}
