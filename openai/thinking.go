package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
	"github.com/weftgo/weft"
)

// ThinkingDialect selects how weft's ThinkingConfig reaches an
// OpenAI-compatible server: the official API takes reasoning_effort,
// while gateways such as z.ai and Moonshot take a thinking object the
// SDK's typed params have no field for. DialectAuto (the default)
// detects from the base URL; the explicit dialects override detection.
type ThinkingDialect int

const (
	DialectAuto ThinkingDialect = iota
	// DialectEffort sends reasoning_effort ("low"|"medium"|"high") —
	// the official Chat Completions knob.
	DialectEffort
	// DialectObject injects thinking:{"type":"enabled"|"disabled"}
	// into the request body — the z.ai and Moonshot shape.
	DialectObject
	// DialectNone never sends thinking parameters, for strict
	// compatible servers that reject unknown fields.
	DialectNone
)

// Dialect pins the thinking wire form instead of detecting it from the
// base URL. It matters for custom gateways behind hosts the adapter
// does not recognize and for clients whose endpoint it cannot inspect.
func Dialect(d ThinkingDialect) Option {
	return optionFunc(func(c *config) { c.dialect = d })
}

// resolveDialect maps DialectAuto onto a concrete dialect from the
// endpoint host: the known gateway hosts (z.ai, bigmodel.cn,
// moonshot.ai/cn) send the thinking object; everything else —
// api.openai.com and unrecognized hosts alike — sends reasoning_effort,
// the OpenAI-compatible contract's own param. An endpoint supplied via
// Client cannot be inspected; DialectAuto still consults
// $OPENAI_BASE_URL there (unset means the SDK default endpoint, i.e.
// effort) — Dialect is the override when the guess misses.
func resolveDialect(d ThinkingDialect, baseURL string) ThinkingDialect {
	if d != DialectAuto {
		return d
	}
	u := baseURL
	if u == "" {
		u = os.Getenv("OPENAI_BASE_URL")
	}
	if u == "" {
		return DialectEffort // the SDK default endpoint, api.openai.com
	}
	host := u
	if parsed, err := url.Parse(u); err == nil && parsed.Host != "" {
		host = parsed.Hostname()
	}
	for _, suffix := range []string{".z.ai", ".bigmodel.cn", ".moonshot.ai", ".moonshot.cn"} {
		if strings.HasSuffix(host, suffix) {
			return DialectObject
		}
	}
	return DialectEffort
}

// reasoningEffort maps the level to Chat Completions' effort strings.
// ThinkOff maps to nothing — the API has no off switch, so the provider
// default stands (a documented gap, not a silent guess); a Budget has
// no effort equivalent and is likewise dropped.
func reasoningEffort(t weft.ThinkingConfig) shared.ReasoningEffort {
	switch t.Level {
	case weft.ThinkLow:
		return shared.ReasoningEffortLow
	case weft.ThinkMedium:
		return shared.ReasoningEffortMedium
	case weft.ThinkHigh:
		return shared.ReasoningEffortHigh
	default:
		return ""
	}
}

// thinkingObj is the gateway wire shape: nil when nothing should be
// sent. A Budget has no representation here yet — Kimi k3's advertised
// think_efforts low/high/max is the future knob (TODO §5.14).
func thinkingObj(t weft.ThinkingConfig) map[string]any {
	switch t.Level {
	case weft.ThinkOff:
		return map[string]any{"type": "disabled"}
	case weft.ThinkLow, weft.ThinkMedium, weft.ThinkHigh:
		return map[string]any{"type": "enabled"}
	default:
		return nil
	}
}

// injectThinking returns a request middleware that adds the thinking
// object to the JSON body. This is the §5.14 escape hatch for gateways
// whose param the SDK's typed request cannot carry: the body is
// rewritten in the open, never absorbed into a fake SDK field.
func injectThinking(thinking map[string]any) option.Middleware {
	return func(r *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("openai adapter: request body is not JSON: %w", err)
		}
		payload["thinking"] = thinking
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		r.Body = io.NopCloser(bytes.NewReader(b))
		r.ContentLength = int64(len(b))
		return next(r)
	}
}
