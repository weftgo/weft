package google

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/weftgo/weft/core"
	"google.golang.org/genai"
)

// The provider's context-window overflow maps to core.ErrContextOverflow
// (ADR 0020 §5): the caller routes it to compaction, and the SDK's own
// error stays reachable underneath for errors.As. The body is the
// provider's documented shape, status 400 INVALID_ARGUMENT with the
// input token count message.
func TestContextOverflowMapsToSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":{"code":400,"message":"* GenerateContentRequest.contents: input token count (2000000) exceeds the maximum (1048576)","status":"INVALID_ARGUMENT"}}`)
	}))
	t.Cleanup(srv.Close)
	m := Model("m", Client(testClient(t, srv.URL)))
	_, err := collect(m, basicReq)
	if !errors.Is(err, core.ErrContextOverflow) {
		t.Fatalf("err = %v (%T), want core.ErrContextOverflow", err, err)
	}
	var apiErr genai.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want genai.APIError reachable underneath the sentinel", err, err)
	}
	if apiErr.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", apiErr.Code)
	}
}

// A 400 that is not an overflow maps to nothing: no false positives on
// other request errors (the review's second rule for this mapping).
func TestOtherBadRequestDoesNotMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":{"code":400,"message":"* GenerateContentRequest.contents: content is not supported for this model","status":"INVALID_ARGUMENT"}}`)
	}))
	t.Cleanup(srv.Close)
	m := Model("m", Client(testClient(t, srv.URL)))
	_, err := collect(m, basicReq)
	if errors.Is(err, core.ErrContextOverflow) {
		t.Fatalf("err = %v; an unrelated 400 must not read as overflow", err)
	}
}
