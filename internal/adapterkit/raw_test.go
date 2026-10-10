package adapterkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// rawLogs is a Logs API provider that asks for everything and keeps
// the raw records' bodies.
type rawLogs struct {
	embedded.LoggerProvider
	mu     sync.Mutex
	bodies []string
}

type rawLogger struct {
	embedded.Logger
	p *rawLogs
}

func (p *rawLogs) Logger(string, ...log.LoggerOption) log.Logger { return rawLogger{p: p} }
func (rawLogger) Enabled(context.Context, log.EnabledParameters) bool {
	return true
}
func (l rawLogger) Emit(ctx context.Context, r log.Record) { l.p.emit(ctx, r) }
func (p *rawLogs) emit(_ context.Context, r log.Record) {
	raw := false
	r.WalkAttributes(func(kv attribute.KeyValue) bool {
		raw = raw || (kv.Key == "weft.record" && kv.Value.AsString() == "raw")
		return true
	})
	if raw {
		p.mu.Lock()
		p.bodies = append(p.bodies, r.Body().AsString())
		p.mu.Unlock()
	}
}

// httpModel performs one HTTP exchange through CaptureRaw inside the
// model call, reading the response the way an SDK does, then answers.
type httpModel struct {
	do   func(ctx context.Context)
	next core.Model
}

func (m httpModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	m.do(ctx)
	return m.next.Stream(ctx, req)
}

func runWith(t *testing.T, lp log.LoggerProvider, do func(ctx context.Context)) {
	t.Helper()
	agt := core.New(httpModel{do: do, next: wefttest.Script(wefttest.Say("ok"))}, core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
}

// Nobody asking, the capture is a pass-through: the very request goes
// on and the very response comes back — no body read, none buffered.
func TestCaptureRawPassThroughWhenNotWanted(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "http://x/v1", strings.NewReader("body"))
	body := io.NopCloser(strings.NewReader("resp"))
	resp := &http.Response{StatusCode: 200, Body: body, Header: http.Header{}}
	got, err := CaptureRaw(req, func(r *http.Request) (*http.Response, error) {
		if r != req {
			t.Error("the request was replaced while nobody wanted raw records")
		}
		return resp, nil
	}, nil)
	if err != nil || got != resp || got.Body != body {
		t.Errorf("response replaced: %v %v", got, err)
	}
}

// Wanted, the request goes on with every byte, the caller reads the
// whole response, and the pair is the first RawCap bytes of each with
// the rest counted — reported once, at EOF.
func TestCaptureRawTeesAndCaps(t *testing.T) {
	lp := &rawLogs{}
	big := strings.Repeat("x", RawCap+100)
	var sent, read string
	runWith(t, lp, func(ctx context.Context) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://x/v1?key=k", strings.NewReader(big))
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("X-Sdk-Secret-Thing", "v")
		resp, err := CaptureRaw(req, func(r *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(r.Body)
			sent = string(b)
			return &http.Response{StatusCode: 201, Header: http.Header{"Content-Type": {"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(big))}, nil
		}, []string{"x-sdk-secret-thing"})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		read = string(b)
		_ = resp.Body.Close()
	})
	if sent != big || read != big {
		t.Fatalf("the exchange changed: sent %d read %d, want %d", len(sent), len(read), len(big))
	}
	if len(lp.bodies) != 1 {
		t.Fatalf("raw records = %d, want 1", len(lp.bodies))
	}
	var d struct {
		Request, Response struct {
			Status         int
			MediaType      string `json:"media_type"`
			Body           string
			Bytes          int64
			TruncatedBytes int64 `json:"truncated_bytes"`
		}
		RedactedHeaders []string `json:"redacted_headers"`
	}
	if err := json.Unmarshal([]byte(lp.bodies[0]), &d); err != nil {
		t.Fatal(err)
	}
	for _, side := range []struct {
		Body           string
		Bytes          int64
		TruncatedBytes int64
	}{{d.Request.Body, d.Request.Bytes, d.Request.TruncatedBytes}, {d.Response.Body, d.Response.Bytes, d.Response.TruncatedBytes}} {
		if len(side.Body) != RawCap || side.Bytes != int64(len(big)) || side.TruncatedBytes != 100 {
			t.Errorf("side = %d bytes kept, %d total, %d cut", len(side.Body), side.Bytes, side.TruncatedBytes)
		}
	}
	if d.Request.MediaType != "application/json" || d.Response.MediaType != "text/event-stream" || d.Response.Status != 201 {
		t.Errorf("media %q / %q status %d", d.Request.MediaType, d.Response.MediaType, d.Response.Status)
	}
	if len(d.RedactedHeaders) != 1 || d.RedactedHeaders[0] != "x-sdk-secret-thing" {
		t.Errorf("redacted_headers = %v", d.RedactedHeaders)
	}
}

// A request body that cannot be read is no capture failure of the
// call's: the request goes on, the read error where the body would have
// surfaced it, and no pair is reported.
func TestCaptureRawDropsOnReadFailure(t *testing.T) {
	lp := &rawLogs{}
	boom := errors.New("boom")
	var gotErr error
	runWith(t, lp, func(ctx context.Context) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://x/v1", io.MultiReader(strings.NewReader("ab"), errReader{boom}))
		_, _ = CaptureRaw(req, func(r *http.Request) (*http.Response, error) {
			b, err := io.ReadAll(r.Body)
			if string(b) != "ab" {
				t.Errorf("forwarded body = %q", b)
			}
			gotErr = err
			return nil, err
		}, nil)
	})
	if !errors.Is(gotErr, boom) {
		t.Errorf("forwarded read error = %v", gotErr)
	}
	if len(lp.bodies) != 0 {
		t.Errorf("a failed capture reported %d pairs", len(lp.bodies))
	}
}

// A transport error is reported as a request without a response; a
// response closed before EOF is reported at Close with what was read.
func TestCaptureRawTransportErrorAndEarlyClose(t *testing.T) {
	lp := &rawLogs{}
	runWith(t, lp, func(ctx context.Context) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://x/a", nil)
		_, _ = CaptureRaw(req, func(*http.Request) (*http.Response, error) { return nil, errors.New("refused") }, nil)
		req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://x/b", nil)
		resp, _ := CaptureRaw(req2, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader([]byte("abcdef")))}, nil
		}, nil)
		buf := make([]byte, 3)
		_, _ = resp.Body.Read(buf)
		_ = resp.Body.Close()
		_ = resp.Body.Close() // reported once
	})
	if len(lp.bodies) != 2 {
		t.Fatalf("raw records = %d, want 2", len(lp.bodies))
	}
	if !strings.Contains(lp.bodies[0], `"response":null`) {
		t.Errorf("transport error record = %s", lp.bodies[0])
	}
	if !strings.Contains(lp.bodies[1], `"body":"abc"`) {
		t.Errorf("early close record = %s", lp.bodies[1])
	}
}
