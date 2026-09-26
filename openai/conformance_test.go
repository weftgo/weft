package openai_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	openaisdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/openai"
	"github.com/weftgo/weft/wefttest/conformance"
)

// fixtureFile maps each conformance case to its recorded SSE file;
// cases that share behaviour share a recording.
func fixtureFile(name string) string {
	switch name {
	case "usage_nonzero", "tool_args_delta":
		return "tool_roundtrip_struct.sse"
	case "never_contract_violation":
		return "text_only.sse"
	default:
		return name + ".sse"
	}
}

const stallChunk = `data: {"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{"content":"one"},"finish_reason":null}]}` + "\n\n"

// newModel wires each case to its fixture: FixtureServer for recorded
// exchanges, StallServer for the never-finishing stream, and the
// token-limit option only for max_tokens. Every case but kill_switch
// drives its fixture through an injected SDK client — a test double, so
// the suite runs under WEFT_MODEL_REQUESTS=deny — while kill_switch
// keeps a self-built client: it is the one case that must prove the
// switch fires before I/O.
func newModel(t *testing.T, name string) weft.Model {
	t.Helper()
	at := func(srv *httptest.Server, opts ...openai.Option) weft.Model {
		c := openaisdk.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
		return openai.Model("m", append([]openai.Option{openai.Client(&c)}, opts...)...)
	}
	switch name {
	case "kill_switch":
		// Any request reaching the server fails the test: the switch
		// must be honoured before I/O, not after.
		return openai.Model("m", openai.BaseURL(conformance.NoRequestServer(t).URL), openai.APIKey("test"))
	case "cancel_mid_stream":
		return at(conformance.StallServer(t, stallChunk))
	case "idle_timeout":
		return at(conformance.StallServer(t, stallChunk), openai.IdleTimeout(200*time.Millisecond))
	case "idle_timeout_before_headers":
		return at(conformance.SilentServer(t), openai.IdleTimeout(200*time.Millisecond))
	case "provider_error":
		// The SDK's own transport retries are off: the case wants the
		// one 429 back as the SDK's error type, retry-after header and
		// all, not two silent sleeps first.
		srv := conformance.ErrorServer(t, 429, http.Header{"Retry-After": {"7"}},
			`{"error":{"message":"slow down","type":"rate_limit_error","param":null,"code":"rate_limit_exceeded"}}`)
		c := openaisdk.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"), option.WithMaxRetries(0))
		return openai.Model("m", openai.Client(&c))
	case "slow_stream":
		done := `data: {"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`
		// Drips every 40ms for 160ms under a 120ms idle timeout: the
		// total exceeds the timeout, each gap does not.
		return at(conformance.SlowServer(t, stallChunk, done, 40*time.Millisecond, 4), openai.IdleTimeout(120*time.Millisecond))
	case "max_tokens":
		return at(conformance.FixtureServer(t, filepath.Join("testdata", fixtureFile(name))), openai.MaxTokens(16))
	case "tool_choice_forcing":
		// The case asserts on the request bytes: a recording fixture,
		// wrapped so the suite can read the bodies back.
		srv, bodies := conformance.RecordingFixtureServer(t, filepath.Join("testdata", fixtureFile(name)))
		return conformance.RecordingModel{Model: at(srv), Bodies: bodies}
	default:
		return at(conformance.FixtureServer(t, filepath.Join("testdata", fixtureFile(name))))
	}
}

// The adapter's offline conformance run (TODO §3.5). Reasoning is off:
// reasoning_content is a compatible-server extension, not an OpenAI
// field — a compatible-server user can turn the cap on.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Caps{Files: true, Sequential: true, ToolArgDeltas: true, ToolChoice: true, Usage: true, ErrorHeaders: true}, newModel)
}
