package openai

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go"
	"github.com/weftgo/weft"
)

// The provider's context-window overflow maps to weft.ErrContextOverflow
// (ADR 0020 §5): the caller routes it to compaction, and the SDK's own
// error stays reachable underneath for errors.As. The body is the
// provider's documented shape, status 400 with code
// context_length_exceeded.
func TestContextOverflowMapsToSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":{"message":"This model's maximum context length is 65536 tokens. However, your messages resulted in 100000 tokens. Please reduce the length of the messages.","type":"invalid_request_error","code":"context_length_exceeded","param":null}}`)
	}))
	t.Cleanup(srv.Close)
	c := testClient(srv)
	m := Model("m", Client(&c))
	_, err := collect(m, basicReq)
	if !errors.Is(err, weft.ErrContextOverflow) {
		t.Fatalf("err = %v (%T), want weft.ErrContextOverflow", err, err)
	}
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *openai.Error reachable underneath the sentinel", err, err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", apiErr.StatusCode)
	}
}

// A 400 that is not an overflow maps to nothing: no false positives on
// other request errors (the review's second rule for this mapping).
func TestOtherBadRequestDoesNotMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":{"message":"Invalid value for parameter 'model'.","type":"invalid_request_error","code":"invalid_value_parameter"}}`)
	}))
	t.Cleanup(srv.Close)
	c := testClient(srv)
	m := Model("m", Client(&c))
	_, err := collect(m, basicReq)
	if errors.Is(err, weft.ErrContextOverflow) {
		t.Fatalf("err = %v; an unrelated 400 must not read as overflow", err)
	}
}
