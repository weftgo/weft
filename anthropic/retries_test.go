package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/weftgo/weft"
)

// MaxRetries(0) switches the SDK's transport retries off — the pairing
// for mw.Retry, so a provider's retry-after is read by weft's classifier
// instead of slept on by the SDK under the idle timer. Absent, the SDK's
// own default (2) applies, so the same 500 draws three requests.
func TestMaxRetriesZeroDisablesSDKRetries(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []Option
		want int32
	}{
		{"zero", []Option{MaxRetries(0)}, 1},
		{"negative_is_zero", []Option{MaxRetries(-1)}, 1},
		{"one", []Option{MaxRetries(1)}, 2},
		{"absent_is_sdk_default", nil, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// MaxRetries composes into the client the adapter builds;
			// an injected Client(c) bypasses construction options, so
			// this test must self-build — and a self-built client is
			// what the kill switch guards. Allow it explicitly: the
			// only endpoint is the loopback httptest server, and the
			// kill-switch test itself keeps a self-built client to
			// prove the switch fires.
			t.Setenv("WEFT_MODEL_REQUESTS", "allow")
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"type":"api_error","message":"boom"}}`))
			}))
			defer srv.Close()
			opts := append([]Option{BaseURL(srv.URL), APIKey("test")}, tc.opts...)
			_, err := weft.New(Model("m", opts...)).Generate(context.Background(), weft.Prompt("hi"))
			if err == nil {
				t.Fatal("a 500 produced no error")
			}
			if got := hits.Load(); got != tc.want {
				t.Errorf("requests = %d, want %d", got, tc.want)
			}
		})
	}
}
