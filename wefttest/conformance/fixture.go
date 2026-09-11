package conformance

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// FixtureServer serves the recorded responses in file, in order, one
// per HTTP request — the offline backbone of an adapter's conformance
// run. The file holds one or more responses separated by a line of the
// form
//
//	=== response 2 ===
//
// (everything before the first separator is response 1). Bodies are
// served verbatim as text/event-stream, the wire format of every
// first-party adapter's streaming endpoint. A request beyond the last
// recorded response fails with 500 and a message naming the file, so an
// under-recorded script is loud, never silently green.
func FixtureServer(t *testing.T, file string) *httptest.Server {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("conformance: read fixture %s: %v", file, err)
	}
	responses := parseFixtures(string(b))
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		if i >= len(responses) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprintf(w, "fixture %s exhausted: request %d, %d response(s) recorded", file, i+1, len(responses))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(responses[i]))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// StallServer writes firstChunk (one complete SSE event, flushed), then
// holds the response open until the request context ends — a stream
// that starts but never finishes, for the cancel_mid_stream and
// idle_timeout cases. The chunk bytes are vendor-specific; the behaviour
// is not.
func StallServer(t *testing.T, firstChunk string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("conformance: response writer cannot flush")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, firstChunk)
		flusher.Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// NoRequestServer fails the test if any request reaches it — the
// backbone of the kill_switch case, which promises that a denied call
// makes no request at all, not merely that it returns the sentinel.
func NoRequestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("conformance: a request reached the server (%s %s); the adapter must check the kill switch before any I/O", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func isResponseSeparator(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "=== response ") && strings.HasSuffix(t, "===")
}

func parseFixtures(s string) []string {
	var (
		responses []string
		cur       []string
	)
	flush := func() {
		// Join reproduces the original blank-line structure exactly;
		// a manufactured trailing newline would become an empty SSE
		// event, which some SDKs (genai) reject.
		responses = append(responses, strings.Join(cur, "\n"))
		cur = nil
	}
	for _, line := range strings.Split(s, "\n") {
		if isResponseSeparator(line) {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	// A file that opens with a separator has an empty first part; drop
	// it so response numbering matches the separators.
	if len(responses) > 1 && strings.TrimSpace(responses[0]) == "" {
		responses = responses[1:]
	}
	return responses
}
